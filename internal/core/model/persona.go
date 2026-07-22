package model

import (
	"slices"
	"strings"
)

// Persona décrit une catégorie d'utilisateur connecté, identifiée par une série
// de filtres d'email. Sa description (Prompt) est injectée dans le prompt
// système de l'agent pour les sessions des utilisateurs correspondants, afin de
// lui donner du contexte sur son interlocuteur.
//
// Une persona est globale par défaut ; renseigner Projects restreint sa portée
// aux projets listés (par slug). Un même utilisateur peut correspondre à
// plusieurs personas : leurs descriptions sont alors toutes injectées.
type Persona struct {
	Name    string
	Prompt  string
	Filters []string
	// Projects restreint la portée de la persona aux projets listés (par ID/
	// slug). Vide ⇒ la persona s'applique à tous les projets.
	Projects []ProjectID
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

// ResolvePrompts retourne les descriptions (Prompt) des personas dont un filtre
// correspond à l'utilisateur et dont la portée inclut projectID, dans l'ordre
// de déclaration. matches capture l'email de l'utilisateur (cf.
// Persona.matchesEmail).
func (ps Personas) ResolvePrompts(projectID ProjectID, matches func(pattern string) bool) []string {
	var out []string
	for _, p := range ps {
		if p.appliesToProject(projectID) && p.matchesEmail(matches) {
			out = append(out, p.Prompt)
		}
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
