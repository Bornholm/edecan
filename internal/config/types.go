// Package config définit le format de configuration YAML d'edecán et son
// chargement (cf. SPEC.md §Configuration).
package config

// Role est le rôle attribué à un utilisateur au sein d'un projet.
type Role string

const (
	RoleUser    Role = "user"
	RoleSupport Role = "support"
)

// Config est la racine du fichier de configuration YAML.
type Config struct {
	Server           ServerConfig            `yaml:"server"`
	IdPs             []IdPConfig             `yaml:"idps"`
	Agents           []AgentConfig           `yaml:"agents"`
	AttachmentStores []AttachmentStoreConfig `yaml:"attachment_stores"`
	TicketBackends   []TicketBackendConfig   `yaml:"ticket_backends"`
	Projects         []ProjectConfig         `yaml:"projects"`
	Personas         []PersonaConfig         `yaml:"personas"`
}

// PersonaConfig décrit une catégorie d'utilisateur connecté, identifiée par une
// série de filtres d'email. Prompt est injecté dans le prompt système de
// l'agent pour donner du contexte sur l'interlocuteur. Projects est optionnel :
// laissé vide, la persona s'applique à tous les projets ; renseigné, il
// restreint sa portée aux projets listés (par slug).
type PersonaConfig struct {
	Name     string   `yaml:"name"`
	Prompt   string   `yaml:"prompt"`
	Filters  []string `yaml:"filters"`
	Projects []string `yaml:"projects"`
}

// ServerConfig regroupe les paramètres d'écoute HTTP et de session.
type ServerConfig struct {
	Addr          string `yaml:"addr"`
	BaseURL       string `yaml:"base_url"`
	SessionSecret string `yaml:"session_secret"`
	SQLitePath    string `yaml:"sqlite_path"`
	// GenerationTimeoutSeconds borne la durée totale de génération d'une réponse
	// de l'agent (streaming SSE) — au delà, un encart d'erreur remplace la bulle
	// plutôt que de laisser l'interface pendre. 0 ⇒ DefaultGenerationTimeoutSeconds.
	GenerationTimeoutSeconds int `yaml:"generation_timeout_seconds"`
	// SSEHeartbeatSeconds est l'intervalle entre deux trames keep-alive SSE,
	// émises pendant les temps morts (appel d'outil long) pour qu'aucun proxy ne
	// coupe une connexion inactive. 0 ⇒ DefaultSSEHeartbeatSeconds.
	SSEHeartbeatSeconds int `yaml:"sse_heartbeat_seconds"`
}

// IdPConfig décrit un fournisseur d'identité. Type vaut "oidc" (par défaut)
// ou "oauth2" — pour un fournisseur sans OIDC (pas de découverte, pas
// d'id_token), dont l'échange est traité à part (cf.
// internal/auth/github.go ; seul GitHub est implémenté pour l'instant
// derrière ce type). Issuer est ignoré pour Type "oauth2".
type IdPConfig struct {
	Name         string `yaml:"name"`
	Type         string `yaml:"type"` // oidc (défaut) | oauth2
	Issuer       string `yaml:"issuer"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	RedirectURL  string `yaml:"redirect_url"`
}

// MCPServerConfig référence un serveur d'outils MCP accessible à l'agent.
// Transport sélectionné par Type :
//   - "http" (défaut) : serveur distant en Streamable HTTP — URL et Headers
//     s'appliquent.
//   - "stdio" : serveur local lancé comme sous-processus, communiquant sur
//     stdin/stdout — Command, Args et Env s'appliquent.
type MCPServerConfig struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"` // http (défaut) | stdio
	// URL et Headers : transport http uniquement. Chaque valeur de Headers est
	// un template Go rendu à chaque requête (cf. internal/infra/llm/mcp.go).
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
	// Command, Args et Env : transport stdio uniquement. Command est
	// l'exécutable du serveur MCP, Args ses arguments, Env les variables
	// d'environnement supplémentaires passées au sous-processus en plus de
	// l'environnement du serveur edecán.
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
}

// AgentConfig décrit une configuration LLM (system prompt + MCP + modèle).
type AgentConfig struct {
	Name     string `yaml:"name"`
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	// BaseURL permet de pointer vers un endpoint compatible (ex: gateway LLM
	// interne, proxy, déploiement auto-hébergé) plutôt que l'API publique par
	// défaut du provider. Optionnel : le provider applique son URL par défaut
	// si vide.
	BaseURL string `yaml:"base_url"`
	// APIKey DOIT être injectée via variable d'environnement, jamais en clair
	// dans le YAML versionné (cf. SPEC §Sécurité).
	APIKey       string            `yaml:"api_key"`
	SystemPrompt string            `yaml:"system_prompt"`
	MCPServers   []MCPServerConfig `yaml:"mcp_servers"`
	// SummaryModel est le modèle utilisé pour résumer le contexte lorsque la
	// fenêtre du modèle principal est presque atteinte (cf. SPEC §Chat, point 11).
	// Si vide, le modèle principal est réutilisé.
	SummaryModel string `yaml:"summary_model"`
	// MaxCompletionTokens borne la longueur de chaque réponse générée par
	// l'agent — un agent de chat ne doit pas produire de pavés de texte.
	// 0 (défaut) ⇒ DefaultMaxCompletionTokens est appliqué.
	MaxCompletionTokens int `yaml:"max_completion_tokens"`
	// MaxSequentialToolCalls borne le nombre d'allers-retours outil↔LLM
	// enchaînés avant que l'agent ne doive produire une réponse à
	// l'utilisateur — garde-fou contre un agent qui multiplie les appels
	// d'outils sans converger. Une fois le plafond atteint, une dernière
	// réponse est forcée à partir du contexte déjà collecté (cf.
	// internal/infra/llm/agent.go). 0 (défaut) ⇒ DefaultMaxSequentialToolCalls.
	MaxSequentialToolCalls int `yaml:"max_sequential_tool_calls"`
	// ToolTimeoutSeconds borne la durée d'un appel d'outil MCP isolé — au delà,
	// l'appel est abandonné et signalé comme échoué, l'agent poursuivant avec
	// une réponse dégradée. 0 (défaut) ⇒ DefaultToolTimeoutSeconds.
	ToolTimeoutSeconds int `yaml:"tool_timeout_seconds"`
	// ReasoningEffort demande au modèle d'exposer son raisonnement au niveau
	// d'effort indiqué : minimal | low | medium | high | xhigh. Vide (défaut) ⇒
	// non demandé (un raisonnement spontané reste néanmoins affiché). Sans effet
	// sur un modèle qui ne supporte pas le raisonnement.
	ReasoningEffort string `yaml:"reasoning_effort"`
}

// DefaultMaxCompletionTokens est la borne appliquée quand AgentConfig.MaxCompletionTokens
// n'est pas renseignée dans le YAML.
const DefaultMaxCompletionTokens = 1024

// DefaultMaxSequentialToolCalls est la borne appliquée quand
// AgentConfig.MaxSequentialToolCalls n'est pas renseignée dans le YAML.
const DefaultMaxSequentialToolCalls = 3

// Valeurs par défaut des réglages de résilience du streaming, appliquées
// quand le YAML ne les renseigne pas (fail-safe : ne casse aucune config
// existante).
const (
	// DefaultToolTimeoutSeconds borne un appel d'outil MCP isolé.
	DefaultToolTimeoutSeconds = 60
	// DefaultGenerationTimeoutSeconds borne la génération complète d'une réponse.
	DefaultGenerationTimeoutSeconds = 120
	// DefaultSSEHeartbeatSeconds est l'intervalle des trames keep-alive SSE.
	DefaultSSEHeartbeatSeconds = 15
)

// TicketBackendConfig décrit un backend de tickets externe.
type TicketBackendConfig struct {
	Name   string               `yaml:"name"`
	Type   string               `yaml:"type"` // gitea | github | redmine | sqlite
	Gitea  *GiteaBackendConfig  `yaml:"gitea"`
	GitHub *GitHubBackendConfig `yaml:"github"`
}

// GiteaBackendConfig contient les paramètres spécifiques à l'adapter Gitea.
// AttachmentStore est optionnel : laissé vide, les pièces jointes utilisent
// le stockage natif de Gitea (comportement historique) ; renseigné, elles
// sont déposées sur le store désigné (cf. attachment_stores) à la place —
// utile pour ne pas faire transiter ce contenu par Gitea (confidentialité).
type GiteaBackendConfig struct {
	BaseURL         string `yaml:"base_url"`
	Token           string `yaml:"token"`
	Owner           string `yaml:"owner"`
	Repo            string `yaml:"repo"`
	AttachmentStore string `yaml:"attachment_store"`
}

// GitHubBackendConfig contient les paramètres spécifiques à l'adapter
// GitHub. BaseURL est optionnel ("https://api.github.com" par défaut, ou
// "https://<host>/api/v3" pour GitHub Enterprise). AttachmentStore est
// obligatoire : GitHub n'offre aucun stockage natif de pièce jointe rattachée
// à une issue/un commentaire (cf. internal/infra/ticket/github, package doc).
type GitHubBackendConfig struct {
	BaseURL         string `yaml:"base_url"`
	Token           string `yaml:"token"`
	Owner           string `yaml:"owner"`
	Repo            string `yaml:"repo"`
	AttachmentStore string `yaml:"attachment_store"`
}

// AttachmentStoreConfig décrit un emplacement de stockage du contenu des
// pièces jointes, externe au backend de tickets (cf.
// internal/core/port.AttachmentStore) — référencé par nom depuis
// GitHubBackendConfig.AttachmentStore / GiteaBackendConfig.AttachmentStore.
type AttachmentStoreConfig struct {
	Name  string                      `yaml:"name"`
	Type  string                      `yaml:"type"` // local | s3
	Local *LocalAttachmentStoreConfig `yaml:"local"`
	S3    *S3AttachmentStoreConfig    `yaml:"s3"`
}

// LocalAttachmentStoreConfig stocke les pièces jointes sur le disque local
// du serveur edecán, sous Directory.
type LocalAttachmentStoreConfig struct {
	Directory string `yaml:"directory"`
}

// S3AttachmentStoreConfig stocke les pièces jointes sur un stockage objet
// compatible S3 (AWS S3, MinIO, ou toute autre implémentation compatible).
// AccessKey/SecretKey DOIVENT être injectées via variable d'environnement,
// jamais en clair dans le YAML versionné (cf. SPEC §Sécurité).
type S3AttachmentStoreConfig struct {
	Endpoint  string `yaml:"endpoint"`
	Bucket    string `yaml:"bucket"`
	Prefix    string `yaml:"prefix"`
	Region    string `yaml:"region"`
	UseSSL    bool   `yaml:"use_ssl"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
}

// MembershipRule associe un pattern d'email à un rôle pour un projet.
// En cas de correspondances multiples, le rôle le plus élevé gagne
// (Support > User — cf. SPEC §Authentification, point 3).
type MembershipRule struct {
	Pattern string `yaml:"pattern"`
	Role    Role   `yaml:"role"`
}

// ProjectConfig décrit un espace de support : agent, backend de tickets et
// règles d'appartenance utilisateur. TicketBackend est optionnel : laissé
// vide, le projet ne propose que l'interface de chat (pas de tickets ni de
// handover) — cf. model.Project.HasTicketBackend.
type ProjectConfig struct {
	Slug          string           `yaml:"slug"`
	Name          string           `yaml:"name"`
	Agent         string           `yaml:"agent"`
	TicketBackend string           `yaml:"ticket_backend"`
	Membership    []MembershipRule `yaml:"membership"`
}
