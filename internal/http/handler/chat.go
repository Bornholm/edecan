package handler

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"edecan/internal/core/model"
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

func buildMessageProps(messages []*model.Message, authorName string) []component.ChatMessageProps {
	props := make([]component.ChatMessageProps, 0, len(messages))
	for _, m := range messages {
		props = append(props, component.ChatMessageProps{
			Role:       string(m.Role),
			Content:    m.Content,
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

	cards, err := h.ticketCards(ctx, slug, project, user, role, "")
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	render(w, r, page.Chat(page.ChatProps{
		ProjectSlug:       slug,
		ProjectName:       project.Name,
		UserDisplayName:   user.DisplayName,
		UserRoleLabel:     userRoleLabel(role, project.Name),
		Projects:          h.projectOptions(user),
		Sessions:          buildSessionEntries(sessions, ""),
		ActiveTicketCount: countActiveTickets(cards),
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
	cards, err := h.ticketCards(ctx, slug, project, user, role, "")
	if err != nil {
		writeServiceError(w, r, err)
		return
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
		ActiveTicketCount: countActiveTickets(cards),
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

func writeAssistantFragment(w http.ResponseWriter, flusher http.Flusher, r *http.Request, content string, tools []string, streaming bool) {
	var buf bytes.Buffer
	props := component.ChatMessageProps{Role: "assistant", Content: content, Tools: tools, IsStreaming: streaming}
	if err := component.ChatMessage(props).Render(r.Context(), &buf); err != nil {
		return
	}
	writeSSE(w, flusher, "message", buf.String())
}

// StreamReply streame la réponse de l'agent token par token via SSE
// (SPEC §Chat, point 6 ; PLAN.md §Phase 4). L'annulation du contexte (client
// déconnecté) est propagée par ChatService.StreamAssistantReply pour éviter
// toute fuite de goroutine.
func (h *Handlers) StreamReply(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := r.PathValue("sessionID")
	user := currentUser(r)
	ctx := r.Context()

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

	chunks, err := h.ChatService.StreamAssistantReply(ctx, sessionID, user)
	if err != nil {
		h.Logger.ErrorContext(ctx, "démarrage du streaming LLM", "error", err)
		writeSSE(w, flusher, "done", "erreur")
		return
	}

	var content strings.Builder
	var tools []string
	for chunk := range chunks {
		if chunk.Err != nil {
			h.Logger.ErrorContext(ctx, "erreur de streaming LLM", "error", chunk.Err)
			break
		}
		if chunk.Tool != nil {
			// Retour visuel d'appel d'outil : la bulle assistant est ré-émise
			// avec la liste des outils au-dessus, en état streaming (l'agent
			// n'a pas encore produit sa réponse finale).
			tools = append(tools, chunk.Tool.Name)
			writeAssistantFragment(w, flusher, r, content.String(), tools, true)
			continue
		}
		content.WriteString(chunk.Content)
		writeAssistantFragment(w, flusher, r, content.String(), tools, !chunk.Done)
		if chunk.Done {
			break
		}
	}

	if content.Len() > 0 {
		if err := h.ChatService.FinalizeReply(ctx, sessionID, content.String()); err != nil {
			h.Logger.ErrorContext(ctx, "persistance de la réponse de l'agent", "error", err)
		}
	}
	writeSSE(w, flusher, "done", "ok")
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
