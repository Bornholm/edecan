package model

import (
	"slices"
	"strings"
)

// Persona décrit une catégorie d'utilisateur connecté, identifiée par une série
// de filtres d'email. Sa description (Prompt) est injectée dans le prompt
// système de l'agent pour les sessions des utilisateurs correspondants, afin de
// lui donner du contexte sur son interlocuteur, et ses serveurs MCP
// (MCPServers) s'ajoutent à ceux de l'agent pour ces mêmes sessions.
//
// Une persona est globale par défaut ; renseigner Projects restreint sa portée
// aux projets listés (par slug). Un même utilisateur peut correspondre à
// plusieurs personas : leurs descriptions sont alors toutes injectées et leurs
// serveurs MCP tous ajoutés.
type Persona struct {
	Name    string
	Prompt  string
	Filters []string
	// Projects restreint la portée de la persona aux projets listés (par ID/
	// slug). Vide ⇒ la persona s'applique à tous les projets.
	Projects []ProjectID
	// MCPServers sont les serveurs d'outils MCP ouverts en plus de ceux de
	// l'agent aux sessions des utilisateurs correspondant à la persona — de
	// quoi réserver des outils à une catégorie d'utilisateurs (ex. outils
	// d'administration pour l'équipe interne).
	MCPServers []MCPServer
}

// appliesToProject indique si la persona s'applique au projet donné.
func (p Persona) appliesToProject(projectID ProjectID) bool {
	if len(p.Projects) == 0 {
		return true
	}
	return slices.Contains(p.Projects, projectID)
}

// matchesEmail indique si l'un des filtres de la persona correspond, matches
// étant fourni par l'appelant (cf. Personas.ResolvePrompts) pour garder la
// primitive de correspondance d'email hors du domaine, à l'image de
// Project.RoleFor.
func (p Persona) matchesEmail(matches func(pattern string) bool) bool {
	return slices.ContainsFunc(p.Filters, matches)
}

// Personas est un ensemble de personas configurées, résolues à la volée pour
// un utilisateur donné.
type Personas []Persona

// Resolve retourne les personas dont un filtre correspond à l'utilisateur et
// dont la portée inclut projectID, dans l'ordre de déclaration. matches
// capture l'email de l'utilisateur (cf. Persona.matchesEmail).
func (ps Personas) Resolve(projectID ProjectID, matches func(pattern string) bool) Personas {
	var out Personas
	for _, p := range ps {
		if p.appliesToProject(projectID) && p.matchesEmail(matches) {
			out = append(out, p)
		}
	}
	return out
}

// Prompts retourne les descriptions (Prompt) non vides des personas, dans
// l'ordre — à appliquer au prompt système via AugmentSystemPrompt.
func (ps Personas) Prompts() []string {
	var out []string
	for _, p := range ps {
		if p.Prompt != "" {
			out = append(out, p.Prompt)
		}
	}
	return out
}

// MCPServers retourne les serveurs MCP déclarés par les personas, concaténés
// dans l'ordre — à fusionner avec ceux de l'agent via MergeMCPServers.
func (ps Personas) MCPServers() []MCPServer {
	var out []MCPServer
	for _, p := range ps {
		out = append(out, p.MCPServers...)
	}
	return out
}

// ResolvePrompts retourne les descriptions des personas correspondantes
// (raccourci Resolve + Prompts).
func (ps Personas) ResolvePrompts(projectID ProjectID, matches func(pattern string) bool) []string {
	return ps.Resolve(projectID, matches).Prompts()
}

// MergeMCPServers ajoute extra à base en ignorant tout serveur dont le nom est
// déjà présent — plusieurs personas correspondantes peuvent déclarer le même
// serveur, et un serveur de persona ne doit jamais redéfinir celui de l'agent
// (base fait autorité). base n'est jamais mutée.
func MergeMCPServers(base, extra []MCPServer) []MCPServer {
	if len(extra) == 0 {
		return base
	}

	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]MCPServer, 0, len(base)+len(extra))
	for _, s := range base {
		seen[s.Name] = struct{}{}
		out = append(out, s)
	}
	for _, s := range extra {
		if _, dup := seen[s.Name]; dup {
			continue
		}
		seen[s.Name] = struct{}{}
		out = append(out, s)
	}
	return out
}

// AugmentSystemPrompt ajoute les descriptions de personas résolues au prompt
// système de base, sous un en-tête dédié. Retourne base inchangé si aucune
// persona ne correspond.
func AugmentSystemPrompt(base string, personaPrompts []string) string {
	if len(personaPrompts) == 0 {
		return base
	}

	var b strings.Builder
	if base != "" {
		b.WriteString(base)
		b.WriteString("\n\n")
	}
	b.WriteString("# Contexte sur l'utilisateur\n\n")
	b.WriteString("Les informations suivantes décrivent l'utilisateur avec qui tu échanges. Tiens-en compte dans tes réponses.\n")
	for _, p := range personaPrompts {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(p))
		b.WriteString("\n")
	}
	return b.String()
}
