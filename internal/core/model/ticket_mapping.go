package model

import "time"

// TicketMapping est l'autorité locale de correspondance entre une session,
// un utilisateur demandeur et un ticket du backend externe
// (SPEC §Data Requirements). C'est elle — et non les métadonnées inscrites
// dans le corps de l'issue — qui fait foi en cas de divergence
// (SPEC §Edge Cases : corps d'issue édité/supprimé dans Gitea).
type TicketMapping struct {
	ID              TicketMappingID
	ProjectID       ProjectID
	TicketBackendID TicketBackendID
	Ref             TicketRef
	RequesterID     UserID
	SessionID       *SessionID // nul pour une création directe de ticket
	CreatedAt       time.Time
}
