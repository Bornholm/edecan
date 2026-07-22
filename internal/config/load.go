package config

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Load lit, interpole les variables d'environnement et valide le fichier de
// configuration YAML situé à path. Toute erreur est fail-fast : edecán ne
// doit jamais démarrer avec une configuration ambiguë ou incomplète
// (cf. SPEC §Edge Cases).
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lecture de la configuration %q: %w", path, err)
	}

	interpolated, err := interpolateEnv(raw)
	if err != nil {
		return nil, fmt.Errorf("interpolation de la configuration %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(interpolated, &cfg); err != nil {
		return nil, fmt.Errorf("parsing de la configuration %q: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("configuration %q invalide: %w", path, err)
	}

	return &cfg, nil
}

// validateMCPServers vérifie les invariants de transport d'une liste de
// serveurs MCP (déclarée par un agent ou une persona) ainsi que l'unicité de
// leurs noms au sein de cette liste — un nom identifie le serveur dans les
// journaux et sert de clé de fusion agent/persona (cf.
// model.MergeMCPServers). scope préfixe les messages d'erreur (« agent "x" »,
// « persona "y" »).
func validateMCPServers(scope string, servers []MCPServerConfig) error {
	names := make(map[string]struct{}, len(servers))
	for _, s := range servers {
		if s.Name == "" {
			return fmt.Errorf("%s: un serveur mcp sans nom a été déclaré", scope)
		}
		if _, dup := names[s.Name]; dup {
			return fmt.Errorf("%s: serveur mcp %q: nom dupliqué", scope, s.Name)
		}
		names[s.Name] = struct{}{}

		switch s.Type {
		case "", "http":
			if s.URL == "" {
				return fmt.Errorf("%s: serveur mcp %q: url requise (transport http)", scope, s.Name)
			}
		case "stdio":
			if s.Command == "" {
				return fmt.Errorf("%s: serveur mcp %q: command requise (transport stdio)", scope, s.Name)
			}
		default:
			return fmt.Errorf("%s: serveur mcp %q: type %q inconnu (http|stdio)", scope, s.Name, s.Type)
		}
	}
	return nil
}

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$|^\*@[^@\s]+\.[^@\s]+$|^[^@\s]*\*[^@\s]*@[^@\s]+\.[^@\s]+$`)

// Validate vérifie la cohérence référentielle et les invariants de la
// configuration (agents/backends référencés, rôles valides, patterns
// d'appartenance bien formés).
func (c *Config) Validate() error {
	if c.Server.Addr == "" {
		return fmt.Errorf("server.addr est requis")
	}
	if c.Server.SQLitePath == "" {
		return fmt.Errorf("server.sqlite_path est requis")
	}
	if len(c.IdPs) == 0 {
		return fmt.Errorf("au moins un fournisseur d'identité (idps) est requis")
	}
	for _, idp := range c.IdPs {
		if idp.Name == "" {
			return fmt.Errorf("un fournisseur d'identité sans nom a été déclaré")
		}
		switch idp.Type {
		case "", "oidc", "oauth2":
		default:
			return fmt.Errorf("idp %q: type %q inconnu (oidc|oauth2)", idp.Name, idp.Type)
		}
		if idp.Type != "oauth2" && idp.Issuer == "" {
			return fmt.Errorf("idp %q: issuer requis (type oidc)", idp.Name)
		}
	}

	agents := make(map[string]struct{}, len(c.Agents))
	for _, a := range c.Agents {
		if a.Name == "" {
			return fmt.Errorf("un agent sans nom a été déclaré")
		}
		if a.Provider == "" {
			return fmt.Errorf("agent %q: provider requis", a.Name)
		}
		if a.Model == "" {
			return fmt.Errorf("agent %q: model requis", a.Name)
		}
		if a.APIKey == "" {
			return fmt.Errorf("agent %q: api_key requis", a.Name)
		}
		if err := validateMCPServers(fmt.Sprintf("agent %q", a.Name), a.MCPServers); err != nil {
			return err
		}
		agents[a.Name] = struct{}{}
	}

	stores := make(map[string]struct{}, len(c.AttachmentStores))
	for _, s := range c.AttachmentStores {
		if s.Name == "" {
			return fmt.Errorf("un emplacement de stockage de pièces jointes sans nom a été déclaré")
		}
		switch s.Type {
		case "local":
			if s.Local == nil {
				return fmt.Errorf("attachment_store %q: type local sans bloc local", s.Name)
			}
			if s.Local.Directory == "" {
				return fmt.Errorf("attachment_store %q: directory requis", s.Name)
			}
		case "s3":
			if s.S3 == nil {
				return fmt.Errorf("attachment_store %q: type s3 sans bloc s3", s.Name)
			}
			if s.S3.Endpoint == "" || s.S3.Bucket == "" {
				return fmt.Errorf("attachment_store %q: endpoint et bucket requis", s.Name)
			}
		default:
			return fmt.Errorf("attachment_store %q: type %q inconnu (local|s3)", s.Name, s.Type)
		}
		stores[s.Name] = struct{}{}
	}

	backends := make(map[string]struct{}, len(c.TicketBackends))
	for _, b := range c.TicketBackends {
		if b.Name == "" {
			return fmt.Errorf("un backend de tickets sans nom a été déclaré")
		}
		switch b.Type {
		case "gitea", "github", "redmine", "sqlite":
		default:
			return fmt.Errorf("backend %q: type %q inconnu (gitea|github|redmine|sqlite)", b.Name, b.Type)
		}
		if b.Type == "gitea" && b.Gitea == nil {
			return fmt.Errorf("backend %q: type gitea sans bloc gitea", b.Name)
		}
		if b.Type == "github" && b.GitHub == nil {
			return fmt.Errorf("backend %q: type github sans bloc github", b.Name)
		}
		if b.Type == "github" {
			if b.GitHub.AttachmentStore == "" {
				return fmt.Errorf("backend %q: attachment_store requis pour le type github (aucun stockage natif)", b.Name)
			}
			if _, ok := stores[b.GitHub.AttachmentStore]; !ok {
				return fmt.Errorf("backend %q: attachment_store %q introuvable", b.Name, b.GitHub.AttachmentStore)
			}
		}
		if b.Type == "gitea" && b.Gitea.AttachmentStore != "" {
			if _, ok := stores[b.Gitea.AttachmentStore]; !ok {
				return fmt.Errorf("backend %q: attachment_store %q introuvable", b.Name, b.Gitea.AttachmentStore)
			}
		}
		backends[b.Name] = struct{}{}
	}

	if len(c.Projects) == 0 {
		return fmt.Errorf("au moins un projet est requis")
	}

	slugs := make(map[string]struct{}, len(c.Projects))
	for _, p := range c.Projects {
		if p.Slug == "" {
			return fmt.Errorf("un projet sans slug a été déclaré")
		}
		if _, dup := slugs[p.Slug]; dup {
			return fmt.Errorf("projet %q: slug dupliqué", p.Slug)
		}
		slugs[p.Slug] = struct{}{}

		if _, ok := agents[p.Agent]; !ok {
			return fmt.Errorf("projet %q: agent %q introuvable", p.Slug, p.Agent)
		}
		// ticket_backend est optionnel : laissé vide, le projet ne propose que
		// l'interface de chat (projet « chat-only »). Renseigné, il doit
		// référencer un backend déclaré.
		if p.TicketBackend != "" {
			if _, ok := backends[p.TicketBackend]; !ok {
				return fmt.Errorf("projet %q: ticket_backend %q introuvable", p.Slug, p.TicketBackend)
			}
		}
		if len(p.Membership) == 0 {
			return fmt.Errorf("projet %q: au moins une règle membership est requise", p.Slug)
		}
		for _, rule := range p.Membership {
			if rule.Pattern == "" || !emailPattern.MatchString(rule.Pattern) {
				return fmt.Errorf("projet %q: pattern d'appartenance %q malformé", p.Slug, rule.Pattern)
			}
			if rule.Role != RoleUser && rule.Role != RoleSupport {
				return fmt.Errorf("projet %q: rôle %q invalide pour le pattern %q (user|support)", p.Slug, rule.Role, rule.Pattern)
			}
		}
	}

	// Les serveurs MCP déclarés par les personas s'ajoutent à ceux de l'agent
	// pour les utilisateurs correspondants (cf. PersonaConfig.MCPServers).
	names := make(map[string]struct{}, len(c.Personas))
	for _, p := range c.Personas {
		if p.Name == "" {
			return fmt.Errorf("une persona sans nom a été déclarée")
		}
		if _, dup := names[p.Name]; dup {
			return fmt.Errorf("persona %q: nom dupliqué", p.Name)
		}
		names[p.Name] = struct{}{}

		// Une persona doit apporter quelque chose : du contexte (prompt), des
		// outils (mcp_servers), ou les deux.
		if p.Prompt == "" && len(p.MCPServers) == 0 {
			return fmt.Errorf("persona %q: prompt ou mcp_servers requis", p.Name)
		}
		if err := validateMCPServers(fmt.Sprintf("persona %q", p.Name), p.MCPServers); err != nil {
			return err
		}
		if len(p.Filters) == 0 {
			return fmt.Errorf("persona %q: au moins un filtre d'email est requis", p.Name)
		}
		for _, f := range p.Filters {
			if f == "" || !emailPattern.MatchString(f) {
				return fmt.Errorf("persona %q: filtre d'email %q malformé", p.Name, f)
			}
		}
		// projects est optionnel (persona globale si vide) ; renseigné, chaque
		// slug doit référencer un projet déclaré.
		for _, slug := range p.Projects {
			if _, ok := slugs[slug]; !ok {
				return fmt.Errorf("persona %q: projet %q introuvable", p.Name, slug)
			}
		}
	}

	return nil
}
