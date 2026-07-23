package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
	"edecan/internal/core/service"
	"edecan/internal/http/view/component"
	"edecan/internal/http/view/page"
)

func parseSessionID(s string) (model.SessionID, error) {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return model.SessionID(v), nil
}

func buildSessionEntries(sessions []*model.Session, activeID string) []page.SessionEntry {
	entries := make([]page.SessionEntry, 0, len(sessions))
	for _, s := range sessions {
		idStr := strconv.FormatUint(uint64(s.ID), 10)
		entry := page.SessionEntry{ID: idStr, Title: s.Title, Active: idStr == activeID}
		if s.ConvertedTicketRef != nil {
			entry.TicketRef = string(*s.ConvertedTicketRef)
		}
		entries = append(entries, entry)
	}
	return entries
}

// buildMessageProps projette l'historique en bulles de conversation. Les tours
// d'outils sont écartés : ils sont persistés pour la mémoire de l'agent, pas
// pour être lus (un message d'appels n'a d'ailleurs aucun contenu à afficher).
func buildMessageProps(messages []*model.Message, authorName string) []component.ChatMessageProps {
	props := make([]component.ChatMessageProps, 0, len(messages))
	for _, m := range messages {
		if m.IsToolTurn() {
			continue
		}
		props = append(props, component.ChatMessageProps{
			Role:       string(m.Role),
			Content:    m.Content,
			Reasoning:  m.Reasoning,
			Timestamp:  m.CreatedAt.Format("15:04"),
			AuthorName: authorName,
		})
	}
	return props
}

// ChatHome affiche la liste des sessions du projet, sans session
// sélectionnée (SPEC §Chat, points 8-9).
func (h *Handlers) ChatHome(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	user := currentUser(r)
	ctx := r.Context()

	project, role, err := h.projectAndRole(ctx, slug, user)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	sessions, err := h.ChatService.ListSessions(ctx, project.ID, user.ID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	// Un projet chat-only n'a pas de tickets : on n'appelle pas le backend.
	var activeTicketCount int
	if project.HasTicketBackend() {
		cards, err := h.ticketCards(ctx, slug, project, user, role, "")
		if err != nil {
			writeServiceError(w, r, err)
			return
		}
		activeTicketCount = countActiveTickets(cards)
	}

	render(w, r, page.Chat(page.ChatProps{
		ProjectSlug:       slug,
		ProjectName:       project.Name,
		UserDisplayName:   user.DisplayName,
		UserRoleLabel:     userRoleLabel(role, project.Name),
		Projects:          h.projectOptions(user),
		Sessions:          buildSessionEntries(sessions, ""),
		HasTickets:        project.HasTicketBackend(),
		ActiveTicketCount: activeTicketCount,
	}))
}

// NewSession démarre une nouvelle session de chat (SPEC §Chat, point 8 :
// plusieurs sessions parallèles par User et par projet).
func (h *Handlers) NewSession(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	user := currentUser(r)
	ctx := r.Context()

	project, _, err := h.projectAndRole(ctx, slug, user)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	sess, err := h.ChatService.StartSession(ctx, project.ID, user.ID, "")
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/projects/%s/chat/%d", slug, sess.ID), http.StatusSeeOther)
}

// DeleteSession supprime définitivement une session et son historique — une
// session déjà convertie en ticket ne peut pas être supprimée (cf.
// ChatService.DeleteSession).
func (h *Handlers) DeleteSession(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	sessionIDStr := r.PathValue("sessionID")
	user := currentUser(r)
	ctx := r.Context()

	sessionID, err := parseSessionID(sessionIDStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := h.ChatService.DeleteSession(ctx, sessionID, user.ID); err != nil {
		if errors.Is(err, service.ErrSessionHasTicket) {
			http.Error(w, "cette session a donné lieu à un ticket et ne peut pas être supprimée", http.StatusConflict)
			return
		}
		writeServiceError(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/projects/%s/chat", slug), http.StatusSeeOther)
}

// SessionView affiche une session de chat et son historique
// (SPEC §Chat, point 9 : reprise de session).
func (h *Handlers) SessionView(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	sessionIDStr := r.PathValue("sessionID")
	user := currentUser(r)
	ctx := r.Context()

	project, role, err := h.projectAndRole(ctx, slug, user)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	sessionID, err := parseSessionID(sessionIDStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	sess, err := h.ChatService.GetSession(ctx, sessionID, user.ID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if sess.ProjectID != project.ID {
		http.NotFound(w, r)
		return
	}

	messages, err := h.ChatService.ListMessages(ctx, sessionID, user.ID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	sessions, err := h.ChatService.ListSessions(ctx, project.ID, user.ID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	flagged, err := h.RelevanceService.AlreadyFlagged(ctx, sessionID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	// Un projet chat-only n'a pas de tickets : on n'appelle pas le backend.
	var activeTicketCount int
	if project.HasTicketBackend() {
		cards, err := h.ticketCards(ctx, slug, project, user, role, "")
		if err != nil {
			writeServiceError(w, r, err)
			return
		}
		activeTicketCount = countActiveTickets(cards)
	}

	entries := buildSessionEntries(sessions, sessionIDStr)
	var current page.SessionEntry
	for _, e := range entries {
		if e.ID == sessionIDStr {
			current = e
		}
	}

	render(w, r, page.Chat(page.ChatProps{
		ProjectSlug:       slug,
		ProjectName:       project.Name,
		UserDisplayName:   user.DisplayName,
		UserRoleLabel:     userRoleLabel(role, project.Name),
		Projects:          h.projectOptions(user),
		Sessions:          entries,
		HasTickets:        project.HasTicketBackend(),
		ActiveTicketCount: activeTicketCount,
		CurrentSession:    &current,
		Messages:          buildMessageProps(messages, user.DisplayName),
		AlreadyFlagged:    flagged,
	}))
}

// PostMessage persiste le message User et retourne le fragment HTMX :
// le message rendu, suivi du placeholder qui ouvrira la connexion SSE de
// la réponse de l'agent (SPEC §Chat, points 6-7).
func (h *Handlers) PostMessage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	sessionIDStr := r.PathValue("sessionID")
	user := currentUser(r)
	ctx := r.Context()

	sessionID, err := parseSessionID(sessionIDStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	content := strings.TrimSpace(r.FormValue("content"))
	if content == "" {
		http.Error(w, "le message ne peut pas être vide", http.StatusBadRequest)
		return
	}

	msg, err := h.ChatService.PostUserMessage(ctx, sessionID, user.ID, content)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	userProps := component.ChatMessageProps{
		Role:       "user",
		Content:    msg.Content,
		Timestamp:  msg.CreatedAt.Format("15:04"),
		AuthorName: user.DisplayName,
	}
	if err := component.ChatMessage(userProps).Render(ctx, w); err != nil {
		h.Logger.ErrorContext(ctx, "rendu du message", "error", err)
		return
	}
	if err := page.AssistantStreamPlaceholder(slug, sessionIDStr).Render(ctx, w); err != nil {
		h.Logger.ErrorContext(ctx, "rendu du placeholder de streaming", "error", err)
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event, data string) {
	fmt.Fprintf(w, "event: %s\n", event)
	for _, line := range strings.Split(data, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprint(w, "\n")
	flusher.Flush()
}

// writeSSEComment écrit un commentaire SSE (ligne débutant par « : ») — trame
// de keep-alive ignorée par le client, qui maintient la connexion ouverte
// pendant les temps morts (appel d'outil long) pour qu'aucun proxy ne la coupe.
func writeSSEComment(w http.ResponseWriter, flusher http.Flusher, text string) {
	fmt.Fprintf(w, ": %s\n\n", text)
	flusher.Flush()
}

func writeAssistantFragment(w http.ResponseWriter, flusher http.Flusher, r *http.Request, content, reasoning string, tools []string, streaming bool) {
	var buf bytes.Buffer
	props := component.ChatMessageProps{Role: "assistant", Content: content, Reasoning: reasoning, Tools: tools, IsStreaming: streaming}
	if err := component.ChatMessage(props).Render(r.Context(), &buf); err != nil {
		return
	}
	writeSSE(w, flusher, "message", buf.String())
}

// writeStreamStatus met à jour la zone de statut de la bulle (event « status »)
// avec un libellé d'activité ; un texte vide efface la zone. timed=true marque
// un statut chronométré côté client (appel d'outil en cours).
func writeStreamStatus(w http.ResponseWriter, flusher http.Flusher, r *http.Request, text string, timed bool) {
	var buf bytes.Buffer
	if err := page.AssistantStreamStatus(text, timed).Render(r.Context(), &buf); err != nil {
		return
	}
	writeSSE(w, flusher, "status", buf.String())
}

// writeStreamError remplace la bulle assistant par un encart d'erreur
// actionnable (event « error » — même cible que « message ») portant un bouton
// « Réessayer ».
func writeStreamError(w http.ResponseWriter, flusher http.Flusher, r *http.Request, slug, sessionID, message string) {
	var buf bytes.Buffer
	if err := page.AssistantStreamError(slug, sessionID, message).Render(r.Context(), &buf); err != nil {
		return
	}
	writeSSE(w, flusher, "error", buf.String())
}

// friendlyStreamError traduit une erreur interne de génération en message
// français actionnable, sans détail technique (l'erreur brute est logguée à
// part par l'appelant). PLAN §1.4.
func friendlyStreamError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "L'agent met trop de temps à répondre. Veuillez réessayer."
	case errors.Is(err, context.Canceled):
		return "La génération a été interrompue. Veuillez réessayer."
	default:
		return "Le service d'IA est momentanément indisponible. Veuillez réessayer."
	}
}

// statusLabel dérive le libellé de la zone de statut d'un changement d'étape.
func statusLabel(stage port.ChatStage) string {
	switch stage {
	case port.StageThinking:
		return "L'agent réfléchit…"
	case port.StageGenerating:
		return "Rédige la réponse…"
	default:
		return ""
	}
}

// StreamReply streame la réponse de l'agent via SSE (SPEC §Chat, point 6 ;
// PLAN-UX-CHAT §Phase 1). La boucle est rendue résiliente :
//   - timeout global de génération (h.StreamGenerationTimeout) — au delà, un
//     encart d'erreur remplace la bulle plutôt que de pendre ;
//   - keep-alive périodique (h.StreamHeartbeat) pendant les temps morts ;
//   - événements typés (message / status / error / done) consommés par
//     l'extension SSE htmx ;
//   - jamais de bulle laissée en état « streaming » (curseur figé) : sur
//     erreur/timeout on émet un encart d'erreur, sur succès la bulle finale
//     est ré-émise sans curseur.
//
// L'annulation du contexte (client déconnecté OU timeout) est propagée à
// StreamAssistantReply, qui arrête sa goroutine productrice — pas de fuite.
func (h *Handlers) StreamReply(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := r.PathValue("sessionID")
	slug := r.PathValue("slug")
	user := currentUser(r)
	reqCtx := r.Context()

	sessionID, err := parseSessionID(sessionIDStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming non supporté", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Timeout global de génération : dérive du contexte de la requête, donc une
	// déconnexion client l'annule aussi. defer cancel() garantit que la
	// goroutine productrice de StreamAssistantReply est bien libérée.
	ctx, cancel := context.WithTimeout(reqCtx, h.StreamGenerationTimeout)
	defer cancel()

	chunks, err := h.ChatService.StreamAssistantReply(ctx, sessionID, user)
	if err != nil {
		h.Logger.ErrorContext(ctx, "démarrage du streaming LLM", "error", err)
		writeStreamError(w, flusher, r, slug, sessionIDStr, friendlyStreamError(err))
		writeSSE(w, flusher, "done", "erreur")
		return
	}

	ticker := time.NewTicker(h.StreamHeartbeat)
	defer ticker.Stop()

	var content strings.Builder
	var reasoning strings.Builder
	var tools []string
	// Tours d'outils résolus pendant la génération, à persister avec la réponse
	// finale (cf. ChatService.FinalizeReply) : sans eux, l'agent perdrait au
	// tour suivant le résultat de ses propres recherches.
	var toolTurns []model.Message
	statusCleared := false
	var fatalErr error

loop:
	for {
		select {
		case <-ctx.Done():
			fatalErr = ctx.Err()
			break loop
		case <-ticker.C:
			writeSSEComment(w, flusher, "ping")
		case chunk, ok := <-chunks:
			if !ok {
				break loop
			}
			switch {
			case chunk.Err != nil:
				fatalErr = chunk.Err
				break loop
			case chunk.Stage != "":
				if !statusCleared {
					writeStreamStatus(w, flusher, r, statusLabel(chunk.Stage), false)
				}
			case chunk.Reasoning != "":
				// Raisonnement (« thinking ») : accumulé et ré-affiché dans la
				// section repliable au-dessus de la bulle, avant tout contenu.
				reasoning.WriteString(chunk.Reasoning)
				writeAssistantFragment(w, flusher, r, content.String(), reasoning.String(), tools, true)
			case chunk.Tool != nil:
				h.handleToolChunk(w, flusher, r, chunk.Tool, &content, &reasoning, &tools)
			case len(chunk.ToolTurn) > 0:
				toolTurns = append(toolTurns, chunk.ToolTurn...)
			default:
				content.WriteString(chunk.Content)
				// Premier contenu : on efface la zone de statut, la bulle prend
				// le relais visuel.
				if !statusCleared {
					writeStreamStatus(w, flusher, r, "", false)
					statusCleared = true
				}
				writeAssistantFragment(w, flusher, r, content.String(), reasoning.String(), tools, !chunk.Done)
				if chunk.Done {
					break loop
				}
			}
		}
	}

	// Client parti (déconnexion) : ne rien écrire, la goroutine productrice est
	// libérée par defer cancel(). On distingue ce cas d'un timeout serveur, qui
	// lui laisse le client connecté et mérite un encart d'erreur.
	if reqCtx.Err() != nil {
		return
	}

	if fatalErr != nil {
		h.Logger.ErrorContext(ctx, "streaming LLM interrompu", "error", fatalErr)
		writeStreamStatus(w, flusher, r, "", false)
		writeStreamError(w, flusher, r, slug, sessionIDStr, friendlyStreamError(fatalErr))
		writeSSE(w, flusher, "done", "erreur")
		return
	}

	writeStreamStatus(w, flusher, r, "", false)
	if content.Len() == 0 {
		// Fin propre mais aucune réponse produite : on n'a rien à persister,
		// mieux vaut proposer un retry qu'une bulle vide.
		writeStreamError(w, flusher, r, slug, sessionIDStr, "L'agent n'a produit aucune réponse. Veuillez réessayer.")
		writeSSE(w, flusher, "done", "erreur")
		return
	}

	if err := h.ChatService.FinalizeReply(ctx, sessionID, content.String(), reasoning.String(), toolTurns); err != nil {
		h.Logger.ErrorContext(ctx, "persistance de la réponse de l'agent", "error", err)
	}
	// Ré-émission de la bulle finale sans curseur (section de raisonnement
	// repliée) : garantit qu'aucun fragment « streaming » ne reste affiché si le
	// flux s'est clos sans fragment Done.
	writeAssistantFragment(w, flusher, r, content.String(), reasoning.String(), tools, false)
	writeSSE(w, flusher, "done", "ok")
}

// handleToolChunk traduit un événement de cycle de vie d'outil en retour
// visuel : pastille d'outil au-dessus de la bulle (début) et libellé de statut.
func (h *Handlers) handleToolChunk(w http.ResponseWriter, flusher http.Flusher, r *http.Request, tool *port.ToolActivity, content, reasoning *strings.Builder, tools *[]string) {
	switch tool.Phase {
	case port.ToolPhaseStart:
		*tools = append(*tools, tool.Name)
		writeAssistantFragment(w, flusher, r, content.String(), reasoning.String(), *tools, true)
		writeStreamStatus(w, flusher, r, fmt.Sprintf("Utilise l'outil « %s »…", tool.Name), true)
	case port.ToolPhaseEnd:
		if tool.Err != nil {
			writeStreamStatus(w, flusher, r, fmt.Sprintf("L'outil « %s » est indisponible, l'agent poursuit…", tool.Name), false)
		} else {
			writeStreamStatus(w, flusher, r, "Analyse des résultats…", false)
		}
	}
}

// RetryReply relance la génération de la réponse de l'agent après un échec :
// comme aucune réponse assistant n'est persistée en cas d'erreur (cf.
// StreamReply), il suffit de renvoyer un nouveau placeholder qui rouvre le
// flux SSE depuis l'historique inchangé (PLAN §1.4).
func (h *Handlers) RetryReply(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	sessionIDStr := r.PathValue("sessionID")
	user := currentUser(r)
	ctx := r.Context()

	sessionID, err := parseSessionID(sessionIDStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Vérifie la propriété de session avant de rouvrir un flux (SPEC §Sécurité).
	if _, err := h.ChatService.GetSession(ctx, sessionID, user.ID); err != nil {
		writeServiceError(w, r, err)
		return
	}

	render(w, r, page.AssistantStreamPlaceholder(slug, sessionIDStr))
}

// HandoverModalHandler ouvre la fenêtre « Transformer en ticket » : affiche
// immédiatement l'état "Analyse de la session…" de la maquette, qui se
// déclenche lui-même vers HandoverDraftHandler une fois inséré dans le DOM
// (hx-trigger="load" — cf. page.HandoverModal).
func (h *Handlers) HandoverModalHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	sessionIDStr := r.PathValue("sessionID")
	user := currentUser(r)
	ctx := r.Context()

	// Le handover produit un ticket : indisponible pour un projet chat-only.
	if !h.ticketsEnabled(slug) {
		http.NotFound(w, r)
		return
	}

	sessionID, err := parseSessionID(sessionIDStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Vérifie la propriété avant d'ouvrir la fenêtre — une session qui
	// n'appartient pas à l'utilisateur ne doit jamais être exposée, même
	// dans cet état transitoire (SPEC §Sécurité).
	if _, err := h.ChatService.GetSession(ctx, sessionID, user.ID); err != nil {
		writeServiceError(w, r, err)
		return
	}

	render(w, r, page.HandoverModal(slug, sessionIDStr))
}

// HandoverDraftHandler génère le brouillon de ticket via le LLM et remplace
// l'état de chargement de la modale par le formulaire éditable
// (SPEC §Handover, point 14).
func (h *Handlers) HandoverDraftHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	sessionIDStr := r.PathValue("sessionID")
	user := currentUser(r)
	ctx := r.Context()

	// Le handover produit un ticket : indisponible pour un projet chat-only.
	if !h.ticketsEnabled(slug) {
		http.NotFound(w, r)
		return
	}

	sessionID, err := parseSessionID(sessionIDStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	title, body, err := h.HandoverService.GenerateDraft(ctx, sessionID, user.ID)
	errMsg := ""
	if err != nil {
		if errors.Is(err, service.ErrForbidden) {
			http.Error(w, "accès refusé", http.StatusForbidden)
			return
		}
		if errors.Is(err, service.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		// Échec de génération du brouillon : brouillon vide éditable + message
		// non bloquant (SPEC §Edge Cases).
		h.Logger.ErrorContext(ctx, "génération du brouillon de ticket", "error", err)
		errMsg = "Génération automatique impossible — vous pouvez rédiger le brouillon manuellement."
	}

	render(w, r, page.HandoverDraftFragment(slug, sessionIDStr, title, body, errMsg))
}

// HandoverSubmit crée le ticket à partir du brouillon validé/édité. Requête
// htmx : succès signalé via l'en-tête HX-Redirect (navigation complète vers
// le ticket créé), erreur réaffichée dans la modale (SPEC §Handover,
// points 16-19).
func (h *Handlers) HandoverSubmit(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	sessionIDStr := r.PathValue("sessionID")
	user := currentUser(r)
	ctx := r.Context()

	// Le handover produit un ticket : indisponible pour un projet chat-only.
	if !h.ticketsEnabled(slug) {
		http.NotFound(w, r)
		return
	}

	sessionID, err := parseSessionID(sessionIDStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	body := strings.TrimSpace(r.FormValue("body"))
	if title == "" || body == "" {
		render(w, r, page.HandoverDraftFragment(slug, sessionIDStr, title, body, "Le titre et le corps sont requis."))
		return
	}

	ticket, err := h.HandoverService.Submit(ctx, sessionID, user, title, body)
	if err != nil {
		if errors.Is(err, service.ErrForbidden) {
			http.Error(w, "accès refusé", http.StatusForbidden)
			return
		}
		if errors.Is(err, service.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		h.Logger.ErrorContext(ctx, "création du ticket (handover)", "error", err)
		render(w, r, page.HandoverDraftFragment(slug, sessionIDStr, title, body, "Création du ticket impossible, veuillez réessayer."))
		return
	}

	w.Header().Set("HX-Redirect", fmt.Sprintf("/projects/%s/tickets/%s", slug, ticket.Ref))
}

// RelevanceFlagHandler persiste le signalement manuel « Cet échange m'a
// aidé » (SPEC §FAQ, points 29-30).
func (h *Handlers) RelevanceFlagHandler(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := r.PathValue("sessionID")
	user := currentUser(r)
	ctx := r.Context()

	sessionID, err := parseSessionID(sessionIDStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := h.RelevanceService.Flag(ctx, sessionID, user.ID); err != nil {
		writeServiceError(w, r, err)
		return
	}

	render(w, r, page.RelevanceConfirmed())
}
