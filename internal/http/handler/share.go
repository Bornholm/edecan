package handler

import (
	"net/http"
	"strings"

	"edecan/internal/core/model"
	"edecan/internal/http/view/component"
	"edecan/internal/http/view/page"
)

// shareURL compose l'URL publique absolue d'un partage à partir de la BaseURL
// configurée et du jeton.
func (h *Handlers) shareURL(token model.ShareToken) string {
	return strings.TrimRight(h.BaseURL, "/") + "/share/" + string(token)
}

// ShareCreateHandler ouvre la fenêtre de partage : crée (ou réutilise) le lien
// public de la session, après vérification de propriété par le service, et rend
// la modale en état « partagé » (PLAN-UX-CHAT §Phase 4.4). Appelé aussi bien par
// le bouton « Partager » de l'en-tête que par « Créer un lien » de l'état
// inactif.
func (h *Handlers) ShareCreateHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	sessionIDStr := r.PathValue("sessionID")
	user := currentUser(r)
	ctx := r.Context()

	sessionID, err := parseSessionID(sessionIDStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	share, err := h.ShareService.CreateOrGet(ctx, sessionID, user.ID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	render(w, r, page.ShareModal(slug, sessionIDStr, h.shareURL(share.Token), true))
}

// ShareRevokeHandler rend la conversation privée (le lien renvoie alors 404) et
// réaffiche la modale en état « non partagé ».
func (h *Handlers) ShareRevokeHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	sessionIDStr := r.PathValue("sessionID")
	user := currentUser(r)
	ctx := r.Context()

	sessionID, err := parseSessionID(sessionIDStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := h.ShareService.Revoke(ctx, sessionID, user.ID); err != nil {
		writeServiceError(w, r, err)
		return
	}

	render(w, r, page.ShareModal(slug, sessionIDStr, "", false))
}

// PublicShareHandler sert la vue publique en lecture seule d'une conversation
// partagée (route publique, hors RequireAuth). Un jeton inconnu ou révoqué
// renvoie 404. Les identités sont anonymisées et aucun horodatage ni
// raisonnement interne n'est exposé (PLAN-UX-CHAT §Phase 4.5).
func (h *Handlers) PublicShareHandler(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	ctx := r.Context()

	// Refuser l'indexation par les moteurs — mécanisme honoré indépendamment du
	// <head> de la page.
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")

	sess, messages, err := h.ShareService.PublicView(ctx, model.ShareToken(token))
	if err != nil {
		// Toute erreur (jeton inconnu, révoqué, session absente) se traduit par
		// un 404 neutre — on ne révèle pas la cause.
		http.NotFound(w, r)
		return
	}

	render(w, r, page.PublicConversation(page.PublicConversationProps{
		Title:    sess.Title,
		Messages: anonymizeMessages(messages),
	}))
}

// anonymizeMessages projette les messages en props d'affichage sans aucune
// donnée identifiante : libellé d'auteur générique, pas d'horodatage, pas de
// raisonnement interne (PLAN-UX-CHAT §Phase 4, points sécurité).
func anonymizeMessages(messages []*model.Message) []component.ChatMessageProps {
	props := make([]component.ChatMessageProps, 0, len(messages))
	for _, m := range messages {
		props = append(props, component.ChatMessageProps{
			Role:       string(m.Role),
			Content:    m.Content,
			AuthorName: "Utilisateur",
		})
	}
	return props
}
