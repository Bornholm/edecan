package port

import (
	"context"

	"edecan/internal/core/model"
)

// ChatChunk est un fragment de réponse LLM streamé token par token
// (SPEC §Chat, point 6). Un fragment porte soit du contenu (Content), soit un
// événement d'appel d'outil (Tool), soit la fin de réponse (Done), soit une
// erreur (Err).
type ChatChunk struct {
	Content string
	// Tool, s'il est non nil, signale que l'agent a déclenché un appel d'outil
	// MCP avant de poursuivre sa réponse — matière à un retour visuel dans le
	// chat pendant la résolution des outils (cf. handler.StreamReply).
	Tool *ToolActivity
	Done bool
	Err  error
}

// ToolActivity décrit l'appel d'un outil MCP par l'agent, pour un retour
// visuel en cours de génération (l'agent interroge des outils avant de
// formuler sa réponse). Éphémère : non persisté avec le message.
type ToolActivity struct {
	Name string
}

// ChatAgent expose l'agent LLM (system prompt + serveurs MCP + paramètres
// modèle) d'un model.Agent (SPEC §Glossaire : Agent).
type ChatAgent interface {
	// StreamReply envoie l'historique de messages à l'agent et retourne un
	// channel de fragments. L'appelant DOIT consommer le channel jusqu'à
	// fermeture ou annulation de ctx pour éviter toute fuite de goroutine
	// (cf. PLAN.md §Phase 4). ctx DOIT porter une MCPIdentity (cf.
	// WithMCPIdentity) si l'agent déclare des serveurs MCP — c'est elle qui
	// scope la connexion MCP (et donc les ressources qu'un serveur MCP gère
	// par session, ex. un bac à sable) à la session de chat courante.
	StreamReply(ctx context.Context, agent model.Agent, history []model.Message) (<-chan ChatChunk, error)

	// Summarize condense les messages antérieurs lorsque la fenêtre de
	// contexte est presque atteinte (SPEC §Chat, point 11).
	Summarize(ctx context.Context, agent model.Agent, history []model.Message) (string, error)

	// DraftTicket génère un brouillon de ticket (titre + corps Markdown) à
	// partir des échanges d'une session (SPEC §Handover, point 14).
	DraftTicket(ctx context.Context, agent model.Agent, history []model.Message) (title string, body string, err error)

	// ForgetSession libère toute ressource associée à sessionID (ex. une
	// connexion MCP ouverte pour cette session, cf. MCPIdentity.SessionID) —
	// sans effet si l'agent n'a aucun état par session (aucun serveur MCP
	// configuré). Appelé lors de la suppression d'une session de chat
	// (cf. ChatService.DeleteSession).
	ForgetSession(sessionID string)
}

// MCPIdentity porte l'identité associée à l'appel courant — exposée au
// templating Go des en-têtes des serveurs MCP (cf. model.MCPServer.Headers),
// pour qu'un serveur MCP scope ses ressources par session ou par
// utilisateur (ex. un workspace de bac à sable shell distinct par session
// de chat).
type MCPIdentity struct {
	SessionID       string
	UserEmail       string
	UserDisplayName string
}

type mcpIdentityContextKey struct{}

// WithMCPIdentity attache id au contexte — lu par le transport HTTP des
// clients MCP (cf. internal/infra/llm) à chaque requête, pas une seule fois
// à la connexion : c'est ce qui garantit que l'appel d'établissement de
// session MCP ("initialize") porte la bonne valeur, puisque edecán établit
// une connexion MCP distincte par session de chat (cf. ChatAgent.StreamReply).
func WithMCPIdentity(ctx context.Context, id MCPIdentity) context.Context {
	return context.WithValue(ctx, mcpIdentityContextKey{}, id)
}

// MCPIdentityFromContext retourne l'identité attachée par WithMCPIdentity,
// ou la valeur zéro si ctx n'en porte aucune.
func MCPIdentityFromContext(ctx context.Context) (MCPIdentity, bool) {
	id, ok := ctx.Value(mcpIdentityContextKey{}).(MCPIdentity)
	return id, ok
}
