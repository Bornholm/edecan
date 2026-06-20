// Package registry construit les objets du domaine et les adapters
// d'infrastructure à partir de la configuration YAML chargée
// (cf. PLAN.md §Phase 0 : la configuration est la source des projets/agents/
// backends, qui ne sont pas persistés en base).
package registry

import (
	"context"
	"fmt"

	"edecan/internal/auth"
	"edecan/internal/config"
	"edecan/internal/core/model"
	"edecan/internal/core/port"
	"edecan/internal/infra/attachment"
	"edecan/internal/infra/llm"
	"edecan/internal/infra/ticket/gitea"
	"edecan/internal/infra/ticket/github"
)

// Registry regroupe les objets domaine et adapters dérivés de la
// configuration, prêts à être injectés dans la couche service.
type Registry struct {
	Projects       []model.Project
	ProjectByID    map[model.ProjectID]model.Project
	Agents         map[model.AgentID]model.Agent
	ChatAgents     map[model.AgentID]port.ChatAgent
	TicketBackends map[model.TicketBackendID]port.TicketBackend
	AuthManager    *auth.Manager
}

// Build construit le Registry depuis cfg. Échoue fail-fast si un IdP est
// inaccessible (découverte OIDC) ou si un provider LLM est inconnu — edecán
// ne doit jamais démarrer avec une configuration partiellement utilisable.
func Build(ctx context.Context, cfg *config.Config) (*Registry, error) {
	r := &Registry{
		ProjectByID:    make(map[model.ProjectID]model.Project, len(cfg.Projects)),
		Agents:         make(map[model.AgentID]model.Agent, len(cfg.Agents)),
		ChatAgents:     make(map[model.AgentID]port.ChatAgent, len(cfg.Agents)),
		TicketBackends: make(map[model.TicketBackendID]port.TicketBackend, len(cfg.TicketBackends)),
	}

	for _, a := range cfg.Agents {
		agent := agentFromConfig(a)
		r.Agents[agent.ID] = agent

		client, err := llm.NewClient(ctx, agent, a.APIKey)
		if err != nil {
			return nil, fmt.Errorf("construction du client LLM pour l'agent %q: %w", a.Name, err)
		}

		// Les serveurs MCP de l'agent ne sont pas résolus ici : chaque
		// session de chat établit sa propre connexion à la demande (cf.
		// llm.ChatAgent), pour que le templating de leurs en-têtes (cf.
		// port.MCPIdentity) puisse réellement scoper les ressources par
		// session. La joignabilité d'un serveur MCP n'est donc plus vérifiée
		// fail-fast au démarrage — une erreur de connexion remonte au
		// premier message d'une session qui en a besoin.
		r.ChatAgents[agent.ID] = llm.NewChatAgent(client, agent.MCPServers)
	}

	stores := make(map[string]port.AttachmentStore, len(cfg.AttachmentStores))
	for _, s := range cfg.AttachmentStores {
		store, err := attachmentStoreFromConfig(s)
		if err != nil {
			return nil, fmt.Errorf("construction de l'emplacement de stockage de pièces jointes %q: %w", s.Name, err)
		}
		stores[s.Name] = store
	}

	for _, b := range cfg.TicketBackends {
		backend, err := ticketBackendFromConfig(b, stores)
		if err != nil {
			return nil, fmt.Errorf("construction du backend de tickets %q: %w", b.Name, err)
		}
		r.TicketBackends[model.TicketBackendID(b.Name)] = backend
	}

	for _, p := range cfg.Projects {
		project := projectFromConfig(p)
		r.Projects = append(r.Projects, project)
		r.ProjectByID[project.ID] = project
	}

	idpConfigs := make([]auth.IdPConfig, 0, len(cfg.IdPs))
	for _, idp := range cfg.IdPs {
		idpConfigs = append(idpConfigs, auth.IdPConfig{
			Name:         idp.Name,
			Type:         idp.Type,
			Issuer:       idp.Issuer,
			ClientID:     idp.ClientID,
			ClientSecret: idp.ClientSecret,
			RedirectURL:  idp.RedirectURL,
		})
	}
	manager, err := auth.NewManager(ctx, idpConfigs)
	if err != nil {
		return nil, fmt.Errorf("initialisation des fournisseurs OIDC: %w", err)
	}
	r.AuthManager = manager

	return r, nil
}

// ProjectsForEmail retourne les projets auxquels email appartient, avec le
// rôle résolu (SPEC §Authentification, points 2-4).
func (r *Registry) ProjectsForEmail(email string) []ProjectAccess {
	var accesses []ProjectAccess
	for _, p := range r.Projects {
		if role, ok := auth.ResolveRole(p, email); ok {
			accesses = append(accesses, ProjectAccess{Project: p, Role: role})
		}
	}
	return accesses
}

// ProjectAccess associe un projet au rôle résolu pour un utilisateur donné.
type ProjectAccess struct {
	Project model.Project
	Role    model.Role
}

func agentFromConfig(a config.AgentConfig) model.Agent {
	mcpServers := make([]model.MCPServer, 0, len(a.MCPServers))
	for _, s := range a.MCPServers {
		transport := model.MCPTransport(s.Type)
		if transport == "" {
			transport = model.MCPTransportHTTP
		}
		mcpServers = append(mcpServers, model.MCPServer{
			Name:      s.Name,
			Transport: transport,
			URL:       s.URL,
			Headers:   s.Headers,
			Command:   s.Command,
			Args:      s.Args,
			Env:       s.Env,
		})
	}
	maxCompletionTokens := a.MaxCompletionTokens
	if maxCompletionTokens == 0 {
		maxCompletionTokens = config.DefaultMaxCompletionTokens
	}
	maxSequentialToolCalls := a.MaxSequentialToolCalls
	if maxSequentialToolCalls == 0 {
		maxSequentialToolCalls = config.DefaultMaxSequentialToolCalls
	}
	return model.Agent{
		ID:                     model.AgentID(a.Name),
		Provider:               a.Provider,
		Model:                  a.Model,
		BaseURL:                a.BaseURL,
		SystemPrompt:           a.SystemPrompt,
		MCPServers:             mcpServers,
		SummaryModel:           a.SummaryModel,
		MaxCompletionTokens:    maxCompletionTokens,
		MaxSequentialToolCalls: maxSequentialToolCalls,
	}
}

func ticketBackendFromConfig(b config.TicketBackendConfig, stores map[string]port.AttachmentStore) (port.TicketBackend, error) {
	switch b.Type {
	case "gitea":
		var store port.AttachmentStore
		if b.Gitea.AttachmentStore != "" {
			store = stores[b.Gitea.AttachmentStore]
		}
		return gitea.New(gitea.Config{
			BaseURL:         b.Gitea.BaseURL,
			Token:           b.Gitea.Token,
			Owner:           b.Gitea.Owner,
			Repo:            b.Gitea.Repo,
			AttachmentStore: store,
		}), nil
	case "github":
		return github.New(github.Config{
			BaseURL:         b.GitHub.BaseURL,
			Token:           b.GitHub.Token,
			Owner:           b.GitHub.Owner,
			Repo:            b.GitHub.Repo,
			AttachmentStore: stores[b.GitHub.AttachmentStore],
		}), nil
	default:
		return nil, fmt.Errorf("type de backend %q non supporté pour l'instant", b.Type)
	}
}

// attachmentStoreFromConfig construit un port.AttachmentStore depuis sa
// configuration (cf. internal/config.AttachmentStoreConfig) — validé en
// amont par config.Validate (type connu, bloc correspondant présent).
func attachmentStoreFromConfig(s config.AttachmentStoreConfig) (port.AttachmentStore, error) {
	switch s.Type {
	case "local":
		return attachment.NewLocalStore(s.Local.Directory)
	case "s3":
		return attachment.NewS3Store(attachment.S3Config{
			Endpoint:  s.S3.Endpoint,
			Bucket:    s.S3.Bucket,
			Prefix:    s.S3.Prefix,
			Region:    s.S3.Region,
			UseSSL:    s.S3.UseSSL,
			AccessKey: s.S3.AccessKey,
			SecretKey: s.S3.SecretKey,
		})
	default:
		return nil, fmt.Errorf("type d'emplacement de stockage %q non supporté", s.Type)
	}
}

func projectFromConfig(p config.ProjectConfig) model.Project {
	rules := make([]model.MembershipRule, 0, len(p.Membership))
	for _, m := range p.Membership {
		rules = append(rules, model.MembershipRule{Pattern: m.Pattern, Role: model.Role(m.Role)})
	}
	return model.Project{
		ID:            model.ProjectID(p.Slug),
		Name:          p.Name,
		AgentID:       model.AgentID(p.Agent),
		TicketBackend: model.TicketBackendID(p.TicketBackend),
		Membership:    rules,
	}
}
