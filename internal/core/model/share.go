package model

import "time"

// ShareToken est le jeton public, cryptographiquement aléatoire, d'un partage
// de conversation. Il n'est JAMAIS dérivé de l'identifiant de session (SPEC
// §Sécurité, PLAN-UX-CHAT §Phase 4) : le connaître ne doit rien révéler des
// autres sessions.
type ShareToken string

// SharedConversation matérialise le partage public en lecture seule d'une
// session de chat. edecán ne duplique aucun contenu : le partage est un simple
// pointeur (session + horodatage) résolu à la volée. SharedAt fige un
// instantané temporel — seuls les messages antérieurs ou contemporains à cette
// date sont exposés, jamais la suite privée de la conversation.
type SharedConversation struct {
	ID        ShareID
	Token     ShareToken
	SessionID SessionID
	CreatedBy UserID
	SharedAt  time.Time
	// RevokedAt, non nul, marque un partage rendu privé : le lien renvoie alors
	// 404 (révocation effective immédiate).
	RevokedAt *time.Time
}

// IsActive indique si le partage est toujours consultable (non révoqué).
func (s SharedConversation) IsActive() bool {
	return s.RevokedAt == nil
}
