package model

import "time"

// TicketStatus est l'état d'un ticket tel que reflété par le backend externe.
type TicketStatus string

const (
	TicketStatusOpen   TicketStatus = "open"
	TicketStatusClosed TicketStatus = "closed"
)

// Ticket est une vue rechargée à la demande depuis le backend externe
// (source de vérité). Aucun champ de cette structure n'est persisté
// localement (cf. SPEC §Principe Architectural Directeur).
type Ticket struct {
	Ref         TicketRef
	Title       string
	Body        string // Markdown
	Status      TicketStatus
	Comments    []Comment
	Attachments []Attachment
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Comment est un commentaire bidirectionnel User ↔ Support, propagé vers le
// backend via API (SPEC §Tickets, point 22-23). Attachments couvre aussi les
// pièces jointes déposées directement dans le backend (hors edecán, ex. un
// agent support répondant depuis l'interface native de Gitea) — le backend
// les rattache au commentaire, pas au ticket (SPEC §Tickets, point 24).
type Comment struct {
	ID                string
	AuthorDisplayName string
	Body              string // Markdown
	CreatedAt         time.Time
	Attachments       []Attachment
}

// Attachment référence un fichier joint, stocké exclusivement dans le
// backend (SPEC §Tickets, point 24). ID est l'identifiant de la pièce jointe
// côté backend — utilisé pour le téléchargement proxifié par edecán
// (cf. TicketBackend.DownloadAttachment), pas pour un accès direct au backend
// depuis le navigateur.
type Attachment struct {
	ID   string
	Name string
	URL  string
	Size int64
}

// NewTicket décrit la création d'un ticket, via handover ou création
// directe (SPEC §Handover, §Tickets).
type NewTicket struct {
	Title string
	Body  string // Markdown, métadonnées demandeur incluses (point 17)
	Owner UserID // demandeur réel — tracé en métadonnées, le ticket est créé
	// sous le compte technique côté backend (SPEC §Glossaire : Compte technique).
}

// NewComment décrit l'ajout d'un commentaire.
type NewComment struct {
	Body string // Markdown
}
