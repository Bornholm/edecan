package model

// MCPTransport identifie le mode de communication avec un serveur MCP.
type MCPTransport string

const (
	// MCPTransportHTTP : serveur distant en Streamable HTTP (URL + Headers).
	MCPTransportHTTP MCPTransport = "http"
	// MCPTransportStdio : serveur local lancé en sous-processus, dialoguant
	// sur stdin/stdout (Command + Args + Env).
	MCPTransportStdio MCPTransport = "stdio"
)

// MCPServer référence un serveur d'outils MCP accessible à l'agent.
type MCPServer struct {
	Name      string
	Transport MCPTransport
	// URL, Headers : transport HTTP.
	URL     string
	Headers map[string]string
	// Command, Args, Env : transport stdio. Env ajoute des variables à
	// l'environnement du sous-processus.
	Command string
	Args    []string
	Env     map[string]string
}

// Agent est une configuration LLM (system prompt + serveurs MCP + paramètres
// modèle) référencée par un projet (SPEC §Glossaire).
type Agent struct {
	ID       AgentID
	Provider string
	Model    string
	// BaseURL pointe vers un endpoint compatible alternatif (gateway interne,
	// proxy, déploiement auto-hébergé) ; vide ⇒ URL par défaut du provider.
	BaseURL      string
	SystemPrompt string
	MCPServers   []MCPServer
	// SummaryModel est le modèle utilisé pour le résumé automatique de
	// contexte (SPEC §Chat, point 11). Vide ⇒ réutiliser Model.
	SummaryModel string
	// MaxCompletionTokens borne la longueur de chaque réponse générée par
	// l'agent (cf. config.AgentConfig.MaxCompletionTokens).
	MaxCompletionTokens int
	// MaxSequentialToolCalls borne le nombre d'allers-retours outil↔LLM
	// enchaînés avant qu'une réponse ne soit forcée pour l'utilisateur
	// (cf. config.AgentConfig.MaxSequentialToolCalls).
	MaxSequentialToolCalls int
}
