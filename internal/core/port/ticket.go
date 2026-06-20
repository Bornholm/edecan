// Package port définit les interfaces (ports) au travers desquelles le
// domaine edecán communique avec l'infrastructure. Aucun type de ce package
// ne doit dépendre d'une bibliothèque d'infrastructure concrète.
package port

import (
	"context"
	"io"

	"edecan/internal/core/model"
)

// TicketBackend abstrait un système externe de tickets (Gitea par défaut ;
// Redmine, SQLite interne en option — cf. SPEC §Glossaire). Le backend est
// la source de vérité unique : edecán ne persiste aucun contenu de ticket.
//
// Toutes les méthodes opèrent sous le compte technique edecán
// (cf. SPEC §Glossaire : Compte technique) ; l'identité du demandeur réel
// est tracée via model.TicketMapping et les métadonnées du corps du ticket.
type TicketBackend interface {
	// Create crée un nouveau ticket et retourne son état initial.
	Create(ctx context.Context, t model.NewTicket) (*model.Ticket, error)

	// Get recharge l'état complet du ticket depuis le backend — lecture à la
	// demande (SPEC §Tickets, point 21). Retourne ErrTicketNotFound si le
	// ticket a été supprimé côté backend.
	Get(ctx context.Context, ref model.TicketRef) (*model.Ticket, error)

	// AddComment ajoute un commentaire Markdown, propagé immédiatement vers
	// le backend (SPEC §Tickets, point 23), et retourne le commentaire créé —
	// notamment son ID, nécessaire pour y attacher des pièces jointes
	// (cf. UploadCommentAttachment).
	AddComment(ctx context.Context, ref model.TicketRef, c model.NewComment) (*model.Comment, error)

	// SetStatus clôture ou rouvre le ticket côté backend. L'autorisation
	// (User : clôture seule ; Support : clôture et réouverture) est vérifiée
	// dans la couche service, jamais ici.
	SetStatus(ctx context.Context, ref model.TicketRef, status model.TicketStatus) error

	// UpdateBody réécrit le corps du ticket — utilisé pour y insérer les liens
	// des pièces jointes téléversées lors de la création initiale, celles-ci
	// ne pouvant être envoyées au backend qu'une fois le ticket créé
	// (SPEC §Tickets, point 24).
	UpdateBody(ctx context.Context, ref model.TicketRef, body string) error

	// UploadAttachment transmet un fichier au backend, rattaché au ticket —
	// utilisé pour les pièces jointes de la création initiale, où aucun
	// commentaire n'existe encore. Aucun fichier n'est conservé sur le
	// système de fichiers edecán (SPEC §Tickets, point 24).
	UploadAttachment(ctx context.Context, ref model.TicketRef, filename string, content io.Reader) (*model.Attachment, error)

	// UploadCommentAttachment transmet un fichier au backend, rattaché à un
	// commentaire précis — c'est ce rattachement, natif côté Gitea, qui rend
	// une pièce jointe visible au bon endroit du fil de discussion (et
	// retrouvable même pour des commentaires ajoutés hors edecán, cf.
	// SPEC §Tickets, point 24).
	UploadCommentAttachment(ctx context.Context, commentID, filename string, content io.Reader) (*model.Attachment, error)

	// DownloadAttachment récupère le contenu d'une pièce jointe directement
	// depuis le backend — edecán ne fait que proxifier le flux, sans jamais
	// l'écrire sur son propre système de fichiers (SPEC §Tickets, point 24).
	// L'appelant doit fermer le ReadCloser retourné.
	DownloadAttachment(ctx context.Context, ref model.TicketRef, attachmentID string) (content io.ReadCloser, filename string, err error)
}

// ErrTicketNotFound signale qu'un ticket mappé localement est introuvable
// côté backend (SPEC §Edge Cases : issue supprimée, mapping orphelin).
var ErrTicketNotFound = errTicketNotFound{}

type errTicketNotFound struct{}

func (errTicketNotFound) Error() string { return "ticket introuvable dans le backend" }
