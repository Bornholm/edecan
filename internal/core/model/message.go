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
	// MessageRoleTool porte le résultat d'un appel d'outil MCP, apparié à
	// l'appel correspondant par ToolCallID. Rôle interne : jamais affiché à
	// l'utilisateur, mais rejoué dans le contexte envoyé au LLM pour que
	// l'agent garde trace, aux tours suivants, de ce que ses recherches ont
	// réellement retourné.
	MessageRoleTool MessageRole = "tool"
)

// ToolCall décrit une invocation d'outil décidée par l'agent. ID est
// l'identifiant attribué par le modèle : c'est lui qui apparie l'appel à son
// résultat (Message.ToolCallID), appariement exigé par les API de complétion.
type ToolCall struct {
	ID   string
	Name string
	// Arguments porte les paramètres de l'appel tels que produits par le
	// modèle, sérialisés en JSON.
	Arguments string
}

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
	// ToolCalls, non vide sur un message d'assistant, porte les appels d'outils
	// décidés par l'agent à ce tour. Un tel message ne porte pas de contenu
	// destiné à l'utilisateur : il n'existe que pour être rejoué dans le
	// contexte du LLM (cf. IsToolTurn).
	ToolCalls []ToolCall
	// ToolCallID et ToolName ne sont renseignés que sur un message
	// MessageRoleTool : identifiant de l'appel auquel ce résultat répond, et
	// nom de l'outil exécuté.
	ToolCallID string
	ToolName   string
	CreatedAt  time.Time
}

// IsToolTurn indique que le message fait partie de la mécanique d'appel
// d'outils (appels décidés par l'agent, ou résultat d'un outil) plutôt que de
// la conversation visible. Ces messages sont persistés et rejoués au LLM, mais
// jamais affichés ni partagés.
func (m Message) IsToolTurn() bool {
	return m.Role == MessageRoleTool || len(m.ToolCalls) > 0
}

// ConversationOnly retourne l'historique privé de ses tours d'outils — pour
// les usages qui raisonnent sur les échanges eux-mêmes (résumé de contexte,
// brouillon de ticket) plutôt que sur le déroulé technique de la génération.
func ConversationOnly(history []Message) []Message {
	out := make([]Message, 0, len(history))
	for _, m := range history {
		if m.IsToolTurn() {
			continue
		}
		out = append(out, m)
	}
	return out
}
