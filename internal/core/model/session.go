package model

import "time"

// SessionStatus est l'état d'une session de chat.
type SessionStatus string

const (
	SessionStatusActive SessionStatus = "active"
)

// Session est une conversation persistée entre un User et l'agent LLM d'un
// projet (SPEC §Glossaire, §Data Requirements).
type Session struct {
	ID                 SessionID
	ProjectID          ProjectID
	UserID             UserID
	Title              string
	Status             SessionStatus
	ConvertedTicketRef *TicketRef // non nul ⇒ "A donné lieu au ticket #N" ; reste éditable.
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// HasBeenConvertedToTicket indique si la session a fait l'objet d'un handover.
func (s Session) HasBeenConvertedToTicket() bool {
	return s.ConvertedTicketRef != nil
}

// LinkTicket enregistre la référence du ticket issu du handover de cette
// session. La session reste éditable (SPEC §Handover, point 19).
func (s *Session) LinkTicket(ref TicketRef) {
	s.ConvertedTicketRef = &ref
}
