package model

import "time"

// MessageRole identifie l'auteur d'un message dans une session.
type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleSystem    MessageRole = "system"
	// MessageRoleSummary marque un résumé automatique des échanges
	// antérieurs, généré lorsque la fenêtre de contexte est presque atteinte
	// (SPEC §Chat, point 11).
	MessageRoleSummary MessageRole = "summary"
)

// Message est un message Markdown échangé au sein d'une Session.
type Message struct {
	ID        MessageID
	SessionID SessionID
	Role      MessageRole
	Content   string // Markdown
	// Reasoning porte, pour un message d'assistant, le raisonnement
	// (« thinking ») exposé par le modèle lorsqu'il en produit — affiché dans
	// une section repliable, jamais renvoyé au LLM dans l'historique (évite
	// d'alourdir le contexte). Vide pour les autres rôles ou modèles.
	Reasoning string
	CreatedAt time.Time
}
