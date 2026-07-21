// Package handler câble les services métier aux requêtes HTTP : pages HTML
// (templ) et fragments HTMX. L'autorisation par projet/rôle est déjà
// appliquée par la couche service ; ce package se limite à l'extraction des
// paramètres de requête, au rendu et à la traduction des erreurs métier en
// codes HTTP (cf. PLAN.md §Phase 1, §Phase 6).
package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/hashicorp/golang-lru/v2/expirable"

	"edecan/internal/auth"
	"edecan/internal/core/model"
	"edecan/internal/core/service"
	"edecan/internal/http/view/component"
	"edecan/internal/http/view/layout"
	"edecan/internal/registry"
)

// Handlers regroupe les dépendances nécessaires aux handlers HTTP.
type Handlers struct {
	Registry         *registry.Registry
	SessionStore     *auth.CookieSessionStore
	Secure           bool
	AuthService      *service.AuthService
	ChatService      *service.ChatService
	TicketService    *service.TicketService
	HandoverService  *service.HandoverService
	RelevanceService *service.RelevanceService
	ShareService     *service.ShareService
	Logger           *slog.Logger
	// BaseURL est l'URL publique absolue du service (cfg.Server.BaseURL), sans
	// slash final — utilisée pour composer les liens de partage
	// (cf. ShareCreateHandler).
	BaseURL string
	// TicketCardsCache met en cache, avec TTL, le calcul coûteux des cartes
	// de ticket (un appel backend par ticket) — cf. ticketCards et
	// NewTicketCardsCache. À construire une seule fois pour tout le
	// processus (cf. cmd/edecan/main.go).
	TicketCardsCache *expirable.LRU[ticketCardsCacheKey, []component.TicketCardProps]
	// StreamGenerationTimeout borne la durée totale d'un flux SSE de réponse
	// de l'agent, StreamHeartbeat l'intervalle des trames keep-alive émises
	// pendant les temps morts (cf. StreamReply, config.ServerConfig).
	StreamGenerationTimeout time.Duration
	StreamHeartbeat         time.Duration
}

// render écrit un templ.Component dans la réponse HTTP.
func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	if err := c.Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "rendu de la page", "error", err)
	}
}

// currentUser retourne l'utilisateur authentifié — RequireAuth garantit sa
// présence sur toute route protégée.
func currentUser(r *http.Request) *model.User {
	return auth.IdentityFromContext(r.Context())
}

// writeServiceError traduit les erreurs métier communes en réponse HTTP
// (SPEC §Sécurité : autorisation systématique côté serveur).
func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		http.Error(w, "introuvable", http.StatusNotFound)
	case errors.Is(err, service.ErrForbidden):
		http.Error(w, "accès refusé", http.StatusForbidden)
	default:
		slog.ErrorContext(r.Context(), "erreur de traitement", "error", err)
		http.Error(w, "erreur interne", http.StatusInternalServerError)
	}
}

// projectAndRole résout le projet par son slug et le rôle de l'utilisateur
// courant. Retourne service.ErrNotFound si le projet est inconnu,
// service.ErrForbidden si l'utilisateur n'en est pas membre — un projet dont
// l'utilisateur n'est pas membre ne doit jamais être exposé (SPEC
// §Authentification, point 4).
func (h *Handlers) projectAndRole(ctx context.Context, slug string, user *model.User) (model.Project, model.Role, error) {
	project, ok := h.Registry.ProjectByID[model.ProjectID(slug)]
	if !ok {
		return model.Project{}, "", service.ErrNotFound
	}
	role, ok := auth.ResolveRole(project, user.Email)
	if !ok {
		return model.Project{}, "", service.ErrForbidden
	}
	return project, role, nil
}

// ticketProjectAndRole résout le projet et le rôle comme projectAndRole, mais
// échoue avec service.ErrNotFound si le projet est « chat-only » (aucun
// backend de tickets). Utilisé par toutes les routes tickets/handover : ces
// ressources ne doivent pas exister pour un projet sans backend, même si un
// utilisateur en devine l'URL.
func (h *Handlers) ticketProjectAndRole(ctx context.Context, slug string, user *model.User) (model.Project, model.Role, error) {
	project, role, err := h.projectAndRole(ctx, slug, user)
	if err != nil {
		return model.Project{}, "", err
	}
	if !project.HasTicketBackend() {
		return model.Project{}, "", service.ErrNotFound
	}
	return project, role, nil
}

// ticketsEnabled indique si le projet slug propose la gestion de tickets
// (backend configuré). Utilisé par les handlers handover, qui ne résolvent
// pas le projet via projectAndRole mais doivent malgré tout refuser un projet
// chat-only.
func (h *Handlers) ticketsEnabled(slug string) bool {
	project, ok := h.Registry.ProjectByID[model.ProjectID(slug)]
	return ok && project.HasTicketBackend()
}

// projectOptions liste les projets accessibles à user, pour le sélecteur de
// projet du rail (cf. layout.Shell) — il n'y a pas de page dédiée pour
// changer de projet.
func (h *Handlers) projectOptions(user *model.User) []layout.ProjectOption {
	accesses := h.Registry.ProjectsForEmail(user.Email)
	opts := make([]layout.ProjectOption, 0, len(accesses))
	for _, a := range accesses {
		opts = append(opts, layout.ProjectOption{Slug: string(a.Project.ID), Name: a.Project.Name})
	}
	return opts
}
