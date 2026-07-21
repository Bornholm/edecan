// Package model contient les entités du domaine edecán, indépendantes de
// toute infrastructure (persistance, backend de tickets, LLM, config).
package model

// Identifiants des entités locales (SQLite/GORM).
type (
	UserID           uint
	SessionID        uint
	MessageID        uint
	TicketMappingID  uint
	RelevanceFlagID  uint
	ShareID          uint
)

// Identifiants des entités issues de la configuration YAML (clés stables,
// non auto-incrémentées : slug ou nom déclaré).
type (
	ProjectID       string
	AgentID         string
	TicketBackendID string
)

// TicketRef est l'identifiant du ticket tel que connu du backend externe
// (ex: numéro d'issue Gitea). Le contenu du ticket n'est jamais stocké
// localement — seul ce identifiant fait l'objet d'un mapping
// (cf. SPEC §Principe Architectural Directeur).
type TicketRef string
