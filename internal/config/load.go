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
		if _, ok := backends[p.TicketBackend]; !ok {
			return fmt.Errorf("projet %q: ticket_backend %q introuvable", p.Slug, p.TicketBackend)
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

	return nil
}
