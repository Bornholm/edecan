// Command edecan démarre la plateforme de support L1 assistée par LLM
// (cf. SPEC.md, PLAN.md).
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"edecan/internal/auth"
	"edecan/internal/build"
	"edecan/internal/config"
	"edecan/internal/core/service"
	"edecan/internal/http/handler"
	"edecan/internal/http/static"
	gormrepo "edecan/internal/infra/persistence/gorm"
	"edecan/internal/registry"
)

func main() {
	configPath := flag.String("config", "config.yaml", "chemin du fichier de configuration YAML")
	envPath := flag.String("env", "", "chemin d'un fichier .env à charger dans l'environnement avant de lire la configuration (optionnel)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	logger.Info("edecán", "version", build.ShortVersion, "build", build.LongVersion)

	if *envPath != "" {
		// godotenv.Load ne remplace pas les variables déjà présentes dans
		// l'environnement réel — celles-ci restent prioritaires sur le
		// fichier .env (utile en production, cf. SPEC §Sécurité).
		if err := godotenv.Load(*envPath); err != nil {
			logger.Error("chargement du fichier .env", "path", *envPath, "error", err)
			os.Exit(1)
		}
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("chargement de la configuration", "error", err)
		os.Exit(1)
	}

	db, err := gormrepo.Open(cfg.Server.SQLitePath)
	if err != nil {
		logger.Error("ouverture de la base SQLite", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	reg, err := registry.Build(ctx, cfg)
	if err != nil {
		logger.Error("construction du registre (projets/agents/backends/OIDC)", "error", err)
		os.Exit(1)
	}

	users := gormrepo.NewUserRepository(db)
	sessions := gormrepo.NewSessionRepository(db)
	messages := gormrepo.NewMessageRepository(db)
	ticketMappings := gormrepo.NewTicketMappingRepository(db)
	relevanceFlags := gormrepo.NewRelevanceFlagRepository(db)
	shares := gormrepo.NewShareRepository(db)

	authService := service.NewAuthService(users)
	chatService := service.NewChatService(sessions, messages, relevanceFlags, reg.ProjectByID, reg.Agents, reg.ChatAgents, reg.Personas, auth.MatchesPattern)
	ticketService := service.NewTicketService(reg.TicketBackends, ticketMappings, reg.ProjectByID)
	handoverService := service.NewHandoverService(sessions, messages, reg.ProjectByID, reg.Agents, reg.ChatAgents, ticketService)
	relevanceService := service.NewRelevanceService(sessions, relevanceFlags)
	shareService := service.NewShareService(shares, sessions, messages)

	secure := strings.HasPrefix(cfg.Server.BaseURL, "https://")
	sessionStore := auth.NewCookieSessionStore([]byte(cfg.Server.SessionSecret), users, secure)

	// Réglages de résilience du streaming SSE — valeurs par défaut sûres si le
	// YAML ne les renseigne pas (fail-safe, cf. config.Default*).
	generationTimeoutSeconds := cfg.Server.GenerationTimeoutSeconds
	if generationTimeoutSeconds == 0 {
		generationTimeoutSeconds = config.DefaultGenerationTimeoutSeconds
	}
	heartbeatSeconds := cfg.Server.SSEHeartbeatSeconds
	if heartbeatSeconds == 0 {
		heartbeatSeconds = config.DefaultSSEHeartbeatSeconds
	}

	h := &handler.Handlers{
		Registry:                reg,
		SessionStore:            sessionStore,
		Secure:                  secure,
		AuthService:             authService,
		ChatService:             chatService,
		TicketService:           ticketService,
		HandoverService:         handoverService,
		RelevanceService:        relevanceService,
		ShareService:            shareService,
		BaseURL:                 cfg.Server.BaseURL,
		Logger:                  logger,
		TicketCardsCache:        handler.NewTicketCardsCache(),
		StreamGenerationTimeout: time.Duration(generationTimeoutSeconds) * time.Second,
		StreamHeartbeat:         time.Duration(heartbeatSeconds) * time.Second,
	}

	mux := http.NewServeMux()

	// Routes publiques — aucune session requise.
	mux.HandleFunc("GET /login", h.LoginPage)
	mux.HandleFunc("GET /auth/{idp}/start", h.StartAuth)
	mux.HandleFunc("GET /auth/{idp}/callback", h.Callback)
	mux.HandleFunc("POST /logout", h.Logout)
	mux.HandleFunc("GET /share/{token}", h.PublicShareHandler)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static.Files)))

	// Routes protégées — aucune ressource accessible sans session valide
	// (SPEC §Sécurité).
	protected := http.NewServeMux()
	protected.HandleFunc("GET /", h.Home)

	protected.HandleFunc("GET /projects/{slug}/chat", h.ChatHome)
	protected.HandleFunc("POST /projects/{slug}/chat/sessions", h.NewSession)
	protected.HandleFunc("GET /projects/{slug}/chat/{sessionID}", h.SessionView)
	protected.HandleFunc("POST /projects/{slug}/chat/{sessionID}/messages", h.PostMessage)
	protected.HandleFunc("GET /projects/{slug}/chat/{sessionID}/stream", h.StreamReply)
	protected.HandleFunc("POST /projects/{slug}/chat/{sessionID}/retry", h.RetryReply)
	protected.HandleFunc("POST /projects/{slug}/chat/{sessionID}/delete", h.DeleteSession)
	protected.HandleFunc("GET /projects/{slug}/chat/{sessionID}/handover/modal", h.HandoverModalHandler)
	protected.HandleFunc("POST /projects/{slug}/chat/{sessionID}/handover/draft", h.HandoverDraftHandler)
	protected.HandleFunc("POST /projects/{slug}/chat/{sessionID}/handover", h.HandoverSubmit)
	protected.HandleFunc("POST /projects/{slug}/chat/{sessionID}/relevance", h.RelevanceFlagHandler)
	protected.HandleFunc("POST /projects/{slug}/chat/{sessionID}/share", h.ShareCreateHandler)
	protected.HandleFunc("POST /projects/{slug}/chat/{sessionID}/share/revoke", h.ShareRevokeHandler)

	protected.HandleFunc("GET /projects/{slug}/tickets", h.TicketsList)
	protected.HandleFunc("GET /projects/{slug}/tickets/new", h.NewTicketFormHandler)
	protected.HandleFunc("POST /projects/{slug}/tickets", h.CreateTicket)
	protected.HandleFunc("GET /projects/{slug}/tickets/{ref}", h.TicketDetailHandler)
	protected.HandleFunc("POST /projects/{slug}/tickets/{ref}/comments", h.AddCommentHandler)
	protected.HandleFunc("POST /projects/{slug}/tickets/{ref}/status", h.SetStatusHandler)
	protected.HandleFunc("GET /projects/{slug}/tickets/{ref}/attachments/{id}", h.DownloadAttachmentHandler)

	mux.Handle("/", auth.RequireAuth(sessionStore)(protected))

	logger.Info("démarrage du serveur", "addr", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, mux); err != nil {
		logger.Error("serveur HTTP", "error", err)
		os.Exit(1)
	}
}
