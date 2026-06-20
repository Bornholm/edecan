package model

import "time"

// RelevanceFlag persiste le signalement manuel « Cet échange m'a aidé »
// (SPEC §FAQ). Aucun déclenchement automatique ni intrusif n'est permis.
type RelevanceFlag struct {
	ID        RelevanceFlagID
	SessionID SessionID
	UserID    UserID
	FlaggedAt time.Time
}
