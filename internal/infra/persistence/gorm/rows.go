// Package gorm fournit les implémentations GORM/SQLite des ports de
// persistance. Les modèles GORM (suffixés Row) sont volontairement séparés
// des entités du domaine (internal/core/model) afin que les tags GORM ne
// polluent pas le domaine et pour faciliter une migration future vers
// PostgreSQL sans réécriture applicative (cf. PLAN.md §Phase 2).
package gorm

import "time"

// UserRow est la projection GORM de model.User.
//
// OIDCSubject et IdPName portent un tag `column` explicite : la convention
// de nommage par défaut de GORM transforme "OIDCSubject"/"IdPName" en
// "o_id_c_subject"/"id_p_name" (acronymes non reconnus), ce qui ne
// correspond pas aux noms de colonnes utilisés dans les requêtes SQL brutes
// des repositories (cf. user_repository.go).
type UserRow struct {
	ID          uint   `gorm:"primaryKey"`
	OIDCSubject string `gorm:"column:oidc_subject;index:idx_user_idp_subject,unique"`
	IdPName     string `gorm:"column:idp_name;index:idx_user_idp_subject,unique"`
	Email       string `gorm:"index"`
	DisplayName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (UserRow) TableName() string { return "users" }

// SessionRow est la projection GORM de model.Session.
type SessionRow struct {
	ID                 uint `gorm:"primaryKey"`
	ProjectID          string `gorm:"index:idx_session_project_user"`
	UserID             uint   `gorm:"index:idx_session_project_user"`
	Title              string
	Status             string
	ConvertedTicketRef *string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (SessionRow) TableName() string { return "sessions" }

// MessageRow est la projection GORM de model.Message.
type MessageRow struct {
	ID        uint `gorm:"primaryKey"`
	SessionID uint `gorm:"index"`
	Role      string
	Content   string
	Reasoning string
	CreatedAt time.Time
}

func (MessageRow) TableName() string { return "messages" }

// TicketMappingRow est la projection GORM de model.TicketMapping.
type TicketMappingRow struct {
	ID              uint `gorm:"primaryKey"`
	ProjectID       string `gorm:"index:idx_mapping_project"`
	TicketBackendID string `gorm:"index:idx_mapping_backend_ref,unique"`
	Ref             string `gorm:"index:idx_mapping_backend_ref,unique"`
	RequesterID     uint   `gorm:"index"`
	SessionID       *uint
	CreatedAt       time.Time
}

func (TicketMappingRow) TableName() string { return "ticket_mappings" }

// RelevanceFlagRow est la projection GORM de model.RelevanceFlag.
type RelevanceFlagRow struct {
	ID        uint `gorm:"primaryKey"`
	SessionID uint `gorm:"index"`
	UserID    uint
	FlaggedAt time.Time
}

func (RelevanceFlagRow) TableName() string { return "relevance_flags" }

// SharedConversationRow est la projection GORM de model.SharedConversation.
// Token porte un index unique (le jeton est la clé d'accès publique) ;
// SessionID est indexé pour retrouver un partage actif existant.
type SharedConversationRow struct {
	ID        uint   `gorm:"primaryKey"`
	Token     string `gorm:"uniqueIndex"`
	SessionID uint   `gorm:"index"`
	CreatedBy uint
	SharedAt  time.Time
	RevokedAt *time.Time
}

func (SharedConversationRow) TableName() string { return "shared_conversations" }
