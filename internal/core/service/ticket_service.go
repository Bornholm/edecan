package service

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"time"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

// TicketService porte les cas d'usage tickets (SPEC §Tickets). Le backend
// externe reste la source de vérité ; ce service n'effectue que
// l'autorisation (cloisonnement User/Support) et la tenue du mapping local.
type TicketService struct {
	backends map[model.TicketBackendID]port.TicketBackend
	mappings port.TicketMappingRepository
	projects map[model.ProjectID]model.Project
}

func NewTicketService(
	backends map[model.TicketBackendID]port.TicketBackend,
	mappings port.TicketMappingRepository,
	projects map[model.ProjectID]model.Project,
) *TicketService {
	return &TicketService{backends: backends, mappings: mappings, projects: projects}
}

func (s *TicketService) backendFor(projectID model.ProjectID) (port.TicketBackend, model.Project, error) {
	project, ok := s.projects[projectID]
	if !ok {
		return nil, model.Project{}, ErrNotFound
	}
	backend, ok := s.backends[project.TicketBackend]
	if !ok {
		return nil, model.Project{}, fmt.Errorf("backend de tickets %q introuvable pour le projet %q", project.TicketBackend, project.ID)
	}
	return backend, project, nil
}

// appendRequesterMetadata inscrit l'identité du demandeur dans le corps du
// ticket (SPEC §Handover, point 17). L'autorité de cette correspondance
// reste la table de mapping locale, pas ce bloc lisible
// (SPEC §Edge Cases : corps d'issue édité/supprimé dans Gitea).
func appendRequesterMetadata(body string, requester *model.User) string {
	return fmt.Sprintf("%s\n\n---\n_Demandeur : %s (%s)_\n", body, requester.DisplayName, requester.Email)
}

// requesterMetadataPattern reconnaît le bloc ajouté par appendRequesterMetadata
// à la fin d'un corps de ticket ou de commentaire.
var requesterMetadataPattern = regexp.MustCompile(`(?s)\n\n---\n_Demandeur : (.+?) \((.+?)\)_\n*$`)

// SplitRequesterMetadata isole le bloc d'identité ajouté par
// appendRequesterMetadata (nom et email du demandeur réel) du texte affiché —
// le backend de tickets ne connaît que le compte technique sous lequel tout
// est créé, donc cette identité réelle est affichée séparément (avatar, nom,
// date) plutôt que dans le texte brut (SPEC §Tickets, point 17). ok est faux
// si body ne porte pas ce bloc (issue ou commentaire créé hors edecán).
func SplitRequesterMetadata(body string) (cleanBody, name, email string, ok bool) {
	loc := requesterMetadataPattern.FindStringSubmatchIndex(body)
	if loc == nil {
		return body, "", "", false
	}
	return body[:loc[0]], body[loc[2]:loc[3]], body[loc[4]:loc[5]], true
}

// CreateDirect crée un ticket — création directe (sessionID nil) ou handover
// (sessionID renseigné) — sous le compte technique, et enregistre le mapping
// local (SPEC §Tickets, point 20 ; §Handover, points 16-18).
func (s *TicketService) CreateDirect(ctx context.Context, projectID model.ProjectID, requester *model.User, title, body string, sessionID *model.SessionID) (*model.Ticket, *model.TicketMapping, error) {
	backend, project, err := s.backendFor(projectID)
	if err != nil {
		return nil, nil, err
	}

	ticket, err := backend.Create(ctx, model.NewTicket{
		Title: title,
		Body:  appendRequesterMetadata(body, requester),
		Owner: requester.ID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("création du ticket: %w", err)
	}

	mapping := &model.TicketMapping{
		ProjectID:       projectID,
		TicketBackendID: project.TicketBackend,
		Ref:             ticket.Ref,
		RequesterID:     requester.ID,
		SessionID:       sessionID,
		CreatedAt:       time.Now(),
	}
	if err := s.mappings.Save(ctx, mapping); err != nil {
		// Le ticket existe côté backend mais le mapping local n'a pas pu être
		// sauvé — dégradé acceptable, à réconcilier (cf. PLAN.md §Phase 5).
		return ticket, nil, fmt.Errorf("ticket %s créé mais mapping non sauvé: %w", ticket.Ref, err)
	}
	return ticket, mapping, nil
}

// authorize vérifie que requester peut accéder au ticket ref du projet
// projectID : un Support voit tous les tickets du projet, un User
// uniquement les siens (SPEC §Tickets, point 28).
func (s *TicketService) authorize(ctx context.Context, projectID model.ProjectID, requester *model.User, role model.Role, ref model.TicketRef) (*model.TicketMapping, model.Project, error) {
	backend, project, err := s.backendFor(projectID)
	_ = backend
	if err != nil {
		return nil, model.Project{}, err
	}

	mapping, err := s.mappings.FindByRef(ctx, project.TicketBackend, ref)
	if err != nil {
		return nil, model.Project{}, err
	}
	if mapping == nil || mapping.ProjectID != projectID {
		return nil, model.Project{}, ErrNotFound
	}
	if role != model.RoleSupport && mapping.RequesterID != requester.ID {
		return nil, model.Project{}, ErrForbidden
	}
	return mapping, project, nil
}

// Get recharge l'état du ticket depuis le backend — lecture à la demande
// (SPEC §Tickets, point 21).
func (s *TicketService) Get(ctx context.Context, projectID model.ProjectID, requester *model.User, role model.Role, ref model.TicketRef) (*model.Ticket, error) {
	_, _, err := s.authorize(ctx, projectID, requester, role, ref)
	if err != nil {
		return nil, err
	}
	backend, _, err := s.backendFor(projectID)
	if err != nil {
		return nil, err
	}
	return backend.Get(ctx, ref)
}

// GetMapping retourne le mapping autorisé pour ref — utilisé pour les
// métadonnées d'affichage (demandeur réel, origine session) qui ne
// proviennent pas du backend mais de l'autorité locale
// (SPEC §Tickets, point 17 ; §Handover, point 19).
func (s *TicketService) GetMapping(ctx context.Context, projectID model.ProjectID, requester *model.User, role model.Role, ref model.TicketRef) (*model.TicketMapping, error) {
	mapping, _, err := s.authorize(ctx, projectID, requester, role, ref)
	if err != nil {
		return nil, err
	}
	return mapping, nil
}

// AddComment propage un commentaire Markdown vers le backend
// (SPEC §Tickets, points 22-23) et retourne le commentaire créé — son ID
// permet d'y rattacher ensuite des pièces jointes (cf. UploadCommentAttachment).
func (s *TicketService) AddComment(ctx context.Context, projectID model.ProjectID, requester *model.User, role model.Role, ref model.TicketRef, body string) (*model.Comment, error) {
	_, _, err := s.authorize(ctx, projectID, requester, role, ref)
	if err != nil {
		return nil, err
	}
	backend, _, err := s.backendFor(projectID)
	if err != nil {
		return nil, err
	}
	return backend.AddComment(ctx, ref, model.NewComment{Body: appendRequesterMetadata(body, requester)})
}

// SetStatus clôture ou rouvre le ticket. Un User peut clôturer ses propres
// tickets mais jamais les rouvrir ; un Support peut les deux
// (SPEC §Tickets, points 25-26).
func (s *TicketService) SetStatus(ctx context.Context, projectID model.ProjectID, requester *model.User, role model.Role, ref model.TicketRef, status model.TicketStatus) error {
	_, _, err := s.authorize(ctx, projectID, requester, role, ref)
	if err != nil {
		return err
	}
	if status == model.TicketStatusOpen && role != model.RoleSupport {
		return ErrForbidden
	}
	backend, _, err := s.backendFor(projectID)
	if err != nil {
		return err
	}
	return backend.SetStatus(ctx, ref, status)
}

// UploadAttachment transmet un fichier au backend — aucun fichier n'est
// conservé sur le système de fichiers edecán (SPEC §Tickets, point 24).
func (s *TicketService) UploadAttachment(ctx context.Context, projectID model.ProjectID, requester *model.User, role model.Role, ref model.TicketRef, filename string, content io.Reader) (*model.Attachment, error) {
	_, _, err := s.authorize(ctx, projectID, requester, role, ref)
	if err != nil {
		return nil, err
	}
	backend, _, err := s.backendFor(projectID)
	if err != nil {
		return nil, err
	}
	return backend.UploadAttachment(ctx, ref, filename, content)
}

// UploadCommentAttachment transmet un fichier au backend, rattaché à un
// commentaire précis du ticket ref — l'autorisation porte sur le ticket, pas
// sur le commentaire (il n'existe pas de notion d'auteur de commentaire côté
// edecán, cf. SPEC §Tickets, point 17). Aucun fichier n'est conservé sur le
// système de fichiers edecán (SPEC §Tickets, point 24).
func (s *TicketService) UploadCommentAttachment(ctx context.Context, projectID model.ProjectID, requester *model.User, role model.Role, ref model.TicketRef, commentID, filename string, content io.Reader) (*model.Attachment, error) {
	_, _, err := s.authorize(ctx, projectID, requester, role, ref)
	if err != nil {
		return nil, err
	}
	backend, _, err := s.backendFor(projectID)
	if err != nil {
		return nil, err
	}
	return backend.UploadCommentAttachment(ctx, commentID, filename, content)
}

// DownloadAttachment relaie le flux d'une pièce jointe depuis le backend —
// edecán ne la stocke jamais localement (SPEC §Tickets, point 24). L'appelant
// doit fermer le ReadCloser retourné.
func (s *TicketService) DownloadAttachment(ctx context.Context, projectID model.ProjectID, requester *model.User, role model.Role, ref model.TicketRef, attachmentID string) (io.ReadCloser, string, error) {
	_, _, err := s.authorize(ctx, projectID, requester, role, ref)
	if err != nil {
		return nil, "", err
	}
	backend, _, err := s.backendFor(projectID)
	if err != nil {
		return nil, "", err
	}
	return backend.DownloadAttachment(ctx, ref, attachmentID)
}

// AppendAttachmentLinks insère des liens de pièces jointes dans le corps du
// ticket, avant le bloc d'identité du demandeur (cf. appendRequesterMetadata)
// — utilisé après la création initiale d'un ticket, les pièces jointes ne
// pouvant être téléversées au backend qu'une fois le ticket créé
// (SPEC §Tickets, point 24).
func (s *TicketService) AppendAttachmentLinks(ctx context.Context, projectID model.ProjectID, requester *model.User, role model.Role, ref model.TicketRef, links []string) error {
	if len(links) == 0 {
		return nil
	}
	_, _, err := s.authorize(ctx, projectID, requester, role, ref)
	if err != nil {
		return err
	}
	backend, _, err := s.backendFor(projectID)
	if err != nil {
		return err
	}
	ticket, err := backend.Get(ctx, ref)
	if err != nil {
		return err
	}
	cleanBody, _, _, _ := SplitRequesterMetadata(ticket.Body)
	for _, link := range links {
		cleanBody += "\n\n" + link
	}
	return backend.UpdateBody(ctx, ref, appendRequesterMetadata(cleanBody, requester))
}

// ListForUser retourne les mappings visibles par requester dans le projet :
// tous pour un Support, les siens seulement pour un User
// (SPEC §Tickets, point 28).
func (s *TicketService) ListForUser(ctx context.Context, projectID model.ProjectID, requester *model.User, role model.Role) ([]*model.TicketMapping, error) {
	if _, ok := s.projects[projectID]; !ok {
		return nil, ErrNotFound
	}
	if role == model.RoleSupport {
		return s.mappings.ListByProject(ctx, projectID)
	}
	return s.mappings.ListByProjectAndUser(ctx, projectID, requester.ID)
}
