package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
	"edecan/internal/core/service"
	"edecan/internal/http/view/component"
	"edecan/internal/http/view/page"
)

// maxAttachmentUploadSize borne la taille d'une pièce jointe téléversée — le
// backend de tickets (Gitea) impose lui-même sa propre limite, mais edecán
// doit éviter qu'une requête trop volumineuse ne sature la mémoire du
// processus avant même d'atteindre cette limite côté backend.
const maxAttachmentUploadSize = 25 << 20 // 25 Mio

// ticketCardsCacheTTL borne la fraîcheur du cache des cartes de ticket
// (cf. ticketCardsCacheKey) — un calcul coûteux (un appel backend par
// ticket) refait à chaque page Chat ou Tickets, alors qu'il ne change que
// rarement à cette échelle de temps (SPEC §Tickets, point 21 : lecture à la
// demande, mais pas nécessairement à chaque rendu).
const ticketCardsCacheTTL = 20 * time.Second

// ticketCardsCacheSize borne le nombre d'entrées en cache (une par
// combinaison projet/utilisateur/rôle) — largement suffisant au regard du
// nombre d'utilisateurs simultanés attendu.
const ticketCardsCacheSize = 256

// ticketCardsCacheKey identifie un jeu de cartes de ticket en cache — un
// User et un Support du même projet ne voient pas les mêmes tickets
// (SPEC §Tickets, point 28), d'où le rôle dans la clé.
type ticketCardsCacheKey struct {
	ProjectID model.ProjectID
	UserID    model.UserID
	Role      model.Role
}

// NewTicketCardsCache construit le cache des cartes de ticket, à brancher
// sur Handlers.TicketCardsCache (un seul cache partagé par le processus).
func NewTicketCardsCache() *expirable.LRU[ticketCardsCacheKey, []component.TicketCardProps] {
	return expirable.NewLRU[ticketCardsCacheKey, []component.TicketCardProps](ticketCardsCacheSize, nil, ticketCardsCacheTTL)
}

// userRoleLabel forme le libellé affiché en pied de rail (cf. maquette :
// "User · Infra").
func userRoleLabel(role model.Role, projectName string) string {
	label := "User"
	if role == model.RoleSupport {
		label = "Support"
	}
	if projectName == "" {
		return label
	}
	return label + " · " + projectName
}

// ticketCards construit les cartes de ticket affichées dans le rail latéral
// et la liste — un User voit les siens, un Support voit tous les tickets du
// projet (SPEC §Tickets, point 28). selectedRef, si non vide, marque la
// carte correspondante comme sélectionnée (vue maître/détail). Le résultat
// (hors marquage de sélection) est mis en cache ticketCardsCacheTTL secondes
// pour éviter de refaire un appel backend par ticket à chaque page Chat ou
// Tickets — cf. invalidateTicketCardsCache pour la fraîcheur immédiate après
// une mutation connue d'edecán (création, commentaire, changement de statut).
func (h *Handlers) ticketCards(ctx context.Context, slug string, project model.Project, user *model.User, role model.Role, selectedRef string) ([]component.TicketCardProps, error) {
	key := ticketCardsCacheKey{ProjectID: project.ID, UserID: user.ID, Role: role}

	cards, ok := h.TicketCardsCache.Get(key)
	if !ok {
		fetched, err := h.fetchTicketCards(ctx, slug, project, user, role)
		if err != nil {
			return nil, err
		}
		cards = fetched
		h.TicketCardsCache.Add(key, cards)
	}

	result := make([]component.TicketCardProps, len(cards))
	copy(result, cards)
	for i := range result {
		result[i].Selected = result[i].TicketRef == selectedRef
	}
	return result, nil
}

// invalidateTicketCardsCache force le recalcul des cartes de ticket au
// prochain accès — appelé après toute action d'edecán susceptible de changer
// leur contenu (nouveau ticket, commentaire, changement de statut), pour ne
// pas faire attendre ticketCardsCacheTTL à l'auteur de l'action lui-même.
func (h *Handlers) invalidateTicketCardsCache(projectID model.ProjectID, userID model.UserID, role model.Role) {
	h.TicketCardsCache.Remove(ticketCardsCacheKey{ProjectID: projectID, UserID: userID, Role: role})
}

// fetchTicketCards effectue le travail coûteux réellement mis en cache par
// ticketCards : un appel backend par ticket mappé (SPEC §Tickets, point 21).
func (h *Handlers) fetchTicketCards(ctx context.Context, slug string, project model.Project, user *model.User, role model.Role) ([]component.TicketCardProps, error) {
	mappings, err := h.TicketService.ListForUser(ctx, project.ID, user, role)
	if err != nil {
		return nil, err
	}

	cards := make([]component.TicketCardProps, 0, len(mappings))
	for _, m := range mappings {
		card := component.TicketCardProps{
			TicketRef:   string(m.Ref),
			Project:     project.Name,
			Href:        fmt.Sprintf("/projects/%s/tickets/%s", slug, m.Ref),
			FromSession: m.SessionID != nil,
			CreatedAt:   m.CreatedAt.Format("02/01/2006 15:04"),
		}

		ticket, err := h.TicketService.Get(ctx, project.ID, user, role, m.Ref)
		switch {
		case err == nil:
			card.Title = ticket.Title
			card.Status = string(ticket.Status)
			card.CommentCount = len(ticket.Comments)
			card.HasAttachments = len(ticket.Attachments) > 0
		case errors.Is(err, port.ErrTicketNotFound):
			// Issue supprimée côté backend mais mapping local présent
			// (SPEC §Edge Cases).
			card.Title = "Ticket introuvable dans le backend"
			card.Status = "pending"
		default:
			h.Logger.ErrorContext(ctx, "lecture du ticket", "ref", m.Ref, "error", err)
			card.Title = "Ticket temporairement indisponible"
			card.Status = "pending"
		}
		cards = append(cards, card)
	}
	return cards, nil
}

// attachmentEntries construit les liens de téléchargement proxifiés pour les
// pièces jointes d'un ticket ou d'un commentaire — y compris celles déposées
// directement dans le backend, hors edecán (SPEC §Tickets, point 24).
func attachmentEntries(slug string, ref model.TicketRef, atts []model.Attachment) []page.AttachmentEntry {
	entries := make([]page.AttachmentEntry, 0, len(atts))
	for _, a := range atts {
		entries = append(entries, page.AttachmentEntry{
			ID:        a.ID,
			Name:      a.Name,
			SizeLabel: formatFileSize(a.Size),
			Href:      fmt.Sprintf("/projects/%s/tickets/%s/attachments/%s", slug, ref, a.ID),
		})
	}
	return entries
}

// formatFileSize affiche une taille de fichier lisible (cf. maquette : "2,4 Mo").
func formatFileSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d o", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %co", float64(size)/float64(div), "kMGT"[exp])
}

// countActiveTickets compte les tickets non clôturés — utilisé pour le badge
// de la navigation Tickets du rail (cf. maquette : pastille numérique).
func countActiveTickets(cards []component.TicketCardProps) int {
	n := 0
	for _, c := range cards {
		if c.Status != "closed" {
			n++
		}
	}
	return n
}

// TicketsList affiche les tickets visibles selon le rôle : un User voit les
// siens, un Support voit tous les tickets du projet (SPEC §Tickets, point 28).
func (h *Handlers) TicketsList(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	user := currentUser(r)
	ctx := r.Context()

	project, role, err := h.ticketProjectAndRole(ctx, slug, user)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	cards, err := h.ticketCards(ctx, slug, project, user, role, "")
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	render(w, r, page.Tickets(page.TicketsProps{
		ProjectSlug:       slug,
		ProjectName:       project.Name,
		UserDisplayName:   user.DisplayName,
		UserRoleLabel:     userRoleLabel(role, project.Name),
		Projects:          h.projectOptions(user),
		Cards:             cards,
		ActiveTicketCount: countActiveTickets(cards),
	}))
}

// NewTicketFormHandler ouvre la modale de création directe de ticket
// (SPEC §Tickets, point 20).
func (h *Handlers) NewTicketFormHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !h.ticketsEnabled(slug) {
		http.NotFound(w, r)
		return
	}
	render(w, r, page.NewTicketModal(slug, ""))
}

// CreateTicket crée un ticket hors chat (SPEC §Tickets, point 20). Requête
// htmx : succès signalé via l'en-tête HX-Redirect (navigation complète vers
// le ticket créé), erreur réaffichée dans la modale — même schéma que
// HandoverSubmit. Les pièces jointes du formulaire sont téléversées juste
// après la création — le backend (Gitea) ne permet d'attacher un fichier à
// une issue qu'une fois celle-ci créée — puis référencées par des liens
// markdown insérés dans le corps du ticket
// (cf. TicketService.AppendAttachmentLinks).
func (h *Handlers) CreateTicket(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	user := currentUser(r)
	ctx := r.Context()

	project, role, err := h.ticketProjectAndRole(ctx, slug, user)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentUploadSize)
	if err := r.ParseMultipartForm(maxAttachmentUploadSize); err != nil {
		render(w, r, page.NewTicketFormFragment(slug, "Fichier trop volumineux ou requête invalide."))
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	body := strings.TrimSpace(r.FormValue("body"))
	if title == "" || body == "" {
		render(w, r, page.NewTicketFormFragment(slug, "Le titre et la description sont requis."))
		return
	}

	ticket, _, err := h.TicketService.CreateDirect(ctx, project.ID, user, title, body, nil)
	if err != nil {
		h.Logger.ErrorContext(ctx, "création directe de ticket", "error", err)
		render(w, r, page.NewTicketFormFragment(slug, "Création du ticket impossible, veuillez réessayer."))
		return
	}

	var fileHeaders []*multipart.FileHeader
	if r.MultipartForm != nil {
		fileHeaders = r.MultipartForm.File["attachments"]
	}
	links := make([]string, 0, len(fileHeaders))
	for _, fh := range fileHeaders {
		file, err := fh.Open()
		if err != nil {
			h.Logger.ErrorContext(ctx, "lecture de la pièce jointe", "ref", ticket.Ref, "error", err)
			continue
		}
		attachment, err := h.TicketService.UploadAttachment(ctx, project.ID, user, role, ticket.Ref, fh.Filename, file)
		file.Close()
		if err != nil {
			h.Logger.ErrorContext(ctx, "téléversement de la pièce jointe", "ref", ticket.Ref, "error", err)
			continue
		}
		links = append(links, fmt.Sprintf("Pièce jointe : [%s](/projects/%s/tickets/%s/attachments/%s) (%s)",
			attachment.Name, slug, ticket.Ref, attachment.ID, formatFileSize(attachment.Size)))
	}
	if len(links) > 0 {
		if err := h.TicketService.AppendAttachmentLinks(ctx, project.ID, user, role, ticket.Ref, links); err != nil {
			h.Logger.ErrorContext(ctx, "insertion des liens de pièces jointes", "ref", ticket.Ref, "error", err)
		}
	}

	h.invalidateTicketCardsCache(project.ID, user.ID, role)
	w.Header().Set("HX-Redirect", fmt.Sprintf("/projects/%s/tickets/%s", slug, ticket.Ref))
}

// TicketDetailHandler recharge l'état complet du ticket depuis le backend —
// lecture à la demande (SPEC §Tickets, point 21).
func (h *Handlers) TicketDetailHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	ref := model.TicketRef(r.PathValue("ref"))
	user := currentUser(r)
	ctx := r.Context()

	project, role, err := h.ticketProjectAndRole(ctx, slug, user)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	ticket, err := h.TicketService.Get(ctx, project.ID, user, role, ref)
	if err != nil {
		if errors.Is(err, port.ErrTicketNotFound) {
			http.Error(w, "ticket introuvable dans le backend", http.StatusNotFound)
			return
		}
		writeServiceError(w, r, err)
		return
	}

	cards, err := h.ticketCards(ctx, slug, project, user, role, string(ref))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	// Le demandeur réel et l'origine (session ou création directe) viennent
	// de l'autorité locale (mapping), pas du backend — celui-ci ne connaît que
	// le compte technique sous lequel tout est créé (SPEC §Tickets, point 17 ;
	// §Handover, point 19).
	requesterName := "Demandeur inconnu"
	requesterEmail := ""
	mapping, err := h.TicketService.GetMapping(ctx, project.ID, user, role, ref)
	if err != nil {
		h.Logger.ErrorContext(ctx, "lecture du mapping de ticket", "ref", ref, "error", err)
	} else if requester, err := h.AuthService.FindByID(ctx, mapping.RequesterID); err != nil {
		h.Logger.ErrorContext(ctx, "lecture du demandeur du ticket", "ref", ref, "error", err)
	} else if requester != nil {
		requesterName = requester.DisplayName
		requesterEmail = requester.Email
	}

	ticketBody, _, _, _ := service.SplitRequesterMetadata(ticket.Body)

	// Chaque commentaire porte la même signature que le ticket (apposée par
	// TicketService.AddComment, quel que soit le rôle de l'auteur) — elle
	// permet de retrouver l'auteur réel et son rôle, le backend ne renvoyant
	// que le compte technique comme auteur.
	comments := make([]page.CommentEntry, 0, len(ticket.Comments))
	for _, c := range ticket.Comments {
		body, name, email, ok := service.SplitRequesterMetadata(c.Body)
		entry := page.CommentEntry{
			Author:      c.AuthorDisplayName,
			Body:        c.Body,
			CreatedAt:   c.CreatedAt.Format("02/01/2006 15:04"),
			Attachments: attachmentEntries(slug, ref, c.Attachments),
		}
		if ok {
			entry.Body = body
			entry.Author = name
			if requesterEmail != "" && email == requesterEmail {
				entry.Role = model.RoleUser
			} else {
				entry.Role = model.RoleSupport
			}
		}
		comments = append(comments, entry)
	}

	attachments := attachmentEntries(slug, ref, ticket.Attachments)

	render(w, r, page.TicketDetail(page.TicketDetailProps{
		ProjectSlug:        slug,
		ProjectName:        project.Name,
		UserDisplayName:    user.DisplayName,
		UserRoleLabel:      userRoleLabel(role, project.Name),
		Projects:           h.projectOptions(user),
		Ref:                string(ref),
		Title:              ticket.Title,
		Body:               ticketBody,
		Status:             ticket.Status,
		Comments:           comments,
		RequesterName:      requesterName,
		RequesterCreatedAt: ticket.CreatedAt.Format("02/01/2006 15:04"),
		FromSession:        mapping != nil && mapping.SessionID != nil,
		Attachments:        attachments,
		Cards:              cards,
		ActiveTicketCount:  countActiveTickets(cards),
		// Un User clôture ses propres tickets (déjà vérifié par l'autorisation
		// de TicketService.Get) ; un Support clôture et rouvre
		// (SPEC §Tickets, points 25-26).
		CanClose:  true,
		CanReopen: role == model.RoleSupport,
	}))
}

// AddCommentHandler propage un commentaire vers le backend
// (SPEC §Tickets, points 22-23). Les pièces jointes sélectionnées dans le
// même formulaire sont téléversées d'abord, puis référencées par un lien
// markdown ajouté au corps du commentaire — il n'existe pas, côté edecán, de
// notion de pièce jointe « flottante » indépendante d'un message : tout
// fichier joint est et reste visuellement rattaché au commentaire en cours
// de rédaction au moment de l'envoi (SPEC §Tickets, point 24).
func (h *Handlers) AddCommentHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	ref := model.TicketRef(r.PathValue("ref"))
	user := currentUser(r)
	ctx := r.Context()

	project, role, err := h.ticketProjectAndRole(ctx, slug, user)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentUploadSize)
	if err := r.ParseMultipartForm(maxAttachmentUploadSize); err != nil {
		http.Error(w, "fichier trop volumineux ou requête invalide", http.StatusBadRequest)
		return
	}

	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		http.Error(w, "le commentaire ne peut pas être vide", http.StatusBadRequest)
		return
	}

	comment, err := h.TicketService.AddComment(ctx, project.ID, user, role, ref, body)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	var fileHeaders []*multipart.FileHeader
	if r.MultipartForm != nil {
		fileHeaders = r.MultipartForm.File["attachments"]
	}
	for _, fh := range fileHeaders {
		file, err := fh.Open()
		if err != nil {
			h.Logger.ErrorContext(ctx, "lecture de la pièce jointe", "ref", ref, "error", err)
			continue
		}
		_, err = h.TicketService.UploadCommentAttachment(ctx, project.ID, user, role, ref, comment.ID, fh.Filename, file)
		file.Close()
		if err != nil {
			h.Logger.ErrorContext(ctx, "téléversement de la pièce jointe du commentaire", "ref", ref, "comment", comment.ID, "error", err)
		}
	}

	h.invalidateTicketCardsCache(project.ID, user.ID, role)
	http.Redirect(w, r, fmt.Sprintf("/projects/%s/tickets/%s", slug, ref), http.StatusSeeOther)
}

// DownloadAttachmentHandler relaie le contenu d'une pièce jointe depuis le
// backend de tickets vers le navigateur, sans jamais le stocker localement
// (SPEC §Tickets, point 24).
func (h *Handlers) DownloadAttachmentHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	ref := model.TicketRef(r.PathValue("ref"))
	attachmentID := r.PathValue("id")
	user := currentUser(r)
	ctx := r.Context()

	project, role, err := h.ticketProjectAndRole(ctx, slug, user)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	content, filename, err := h.TicketService.DownloadAttachment(ctx, project.ID, user, role, ref, attachmentID)
	if err != nil {
		if errors.Is(err, port.ErrTicketNotFound) {
			http.Error(w, "pièce jointe introuvable dans le backend", http.StatusNotFound)
			return
		}
		writeServiceError(w, r, err)
		return
	}
	defer content.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	io.Copy(w, content)
}

// SetStatusHandler clôture ou rouvre le ticket — un User ne peut jamais
// rouvrir (SPEC §Tickets, points 25-26 ; §Acceptance Criteria : requête
// forgée → 403).
func (h *Handlers) SetStatusHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	ref := model.TicketRef(r.PathValue("ref"))
	user := currentUser(r)
	ctx := r.Context()

	project, role, err := h.ticketProjectAndRole(ctx, slug, user)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	status := model.TicketStatus(r.FormValue("status"))
	if status != model.TicketStatusOpen && status != model.TicketStatusClosed {
		http.Error(w, "statut invalide", http.StatusBadRequest)
		return
	}

	if err := h.TicketService.SetStatus(ctx, project.ID, user, role, ref, status); err != nil {
		if errors.Is(err, service.ErrForbidden) {
			http.Error(w, "accès refusé", http.StatusForbidden)
			return
		}
		writeServiceError(w, r, err)
		return
	}

	h.invalidateTicketCardsCache(project.ID, user.ID, role)
	http.Redirect(w, r, fmt.Sprintf("/projects/%s/tickets/%s", slug, ref), http.StatusSeeOther)
}
