package model

import (
	"path"
	"strings"
	"testing"
)

// matcherFor imite auth.MatchesPattern (glob simple) pour les besoins du test,
// sans dépendre du package auth.
func matcherFor(email string) func(pattern string) bool {
	return func(pattern string) bool {
		ok, err := path.Match(pattern, strings.ToLower(email))
		return err == nil && ok
	}
}

func TestPersonasResolvePrompts(t *testing.T) {
	personas := Personas{
		{Name: "global", Prompt: "global-ctx", Filters: []string{"*@exemple.com"}},
		{Name: "premium", Prompt: "premium-ctx", Filters: []string{"jean@exemple.com"}, Projects: []ProjectID{"projet-a"}},
		{Name: "autre-projet", Prompt: "autre-ctx", Filters: []string{"*@exemple.com"}, Projects: []ProjectID{"projet-b"}},
	}

	cases := []struct {
		name    string
		email   string
		project ProjectID
		want    []string
	}{
		{
			name:    "persona globale + persona projet cumulées",
			email:   "jean@exemple.com",
			project: "projet-a",
			want:    []string{"global-ctx", "premium-ctx"},
		},
		{
			name:    "persona projet ignorée si le filtre ne correspond pas",
			email:   "marie@exemple.com",
			project: "projet-a",
			want:    []string{"global-ctx"},
		},
		{
			name:    "persona projet ignorée hors de sa portée",
			email:   "jean@exemple.com",
			project: "projet-c",
			want:    []string{"global-ctx"},
		},
		{
			name:    "portée projet respectée",
			email:   "jean@exemple.com",
			project: "projet-b",
			want:    []string{"global-ctx", "autre-ctx"},
		},
		{
			name:    "aucun filtre ne correspond",
			email:   "jean@ailleurs.com",
			project: "projet-a",
			want:    nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := personas.ResolvePrompts(tc.project, matcherFor(tc.email))
			if len(got) != len(tc.want) {
				t.Fatalf("ResolvePrompts = %v, attendu %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ResolvePrompts[%d] = %q, attendu %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestPersonasResolveMCPServers(t *testing.T) {
	personas := Personas{
		{Name: "global", Prompt: "global-ctx", Filters: []string{"*@exemple.com"},
			MCPServers: []MCPServer{{Name: "docs"}}},
		{Name: "interne", Filters: []string{"*@interne.com"},
			MCPServers: []MCPServer{{Name: "admin"}, {Name: "docs"}}},
		{Name: "autre-projet", Filters: []string{"*@interne.com"}, Projects: []ProjectID{"projet-b"},
			MCPServers: []MCPServer{{Name: "hors-portee"}}},
	}

	cases := []struct {
		name    string
		email   string
		project ProjectID
		want    []string // noms attendus, dans l'ordre
	}{
		{
			name:    "serveurs de la persona correspondante",
			email:   "jean@exemple.com",
			project: "projet-a",
			want:    []string{"docs"},
		},
		{
			name:    "persona hors portée ignorée",
			email:   "jean@interne.com",
			project: "projet-a",
			want:    []string{"admin", "docs"},
		},
		{
			name:    "personas cumulées dans la portée",
			email:   "jean@interne.com",
			project: "projet-b",
			want:    []string{"admin", "docs", "hors-portee"},
		},
		{
			name:    "aucun filtre ne correspond",
			email:   "jean@ailleurs.com",
			project: "projet-a",
			want:    nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := personas.Resolve(tc.project, matcherFor(tc.email)).MCPServers()
			if len(got) != len(tc.want) {
				t.Fatalf("MCPServers() = %v, attendu %v", got, tc.want)
			}
			for i := range got {
				if got[i].Name != tc.want[i] {
					t.Fatalf("MCPServers()[%d] = %q, attendu %q", i, got[i].Name, tc.want[i])
				}
			}
		})
	}
}

// TestPersonasPromptsIgnoreVides : une persona qui n'apporte que des outils
// (prompt vide) ne doit pas injecter de bloc de contexte vide.
func TestPersonasPromptsIgnoreVides(t *testing.T) {
	personas := Personas{
		{Name: "outils", Filters: []string{"*@exemple.com"}, MCPServers: []MCPServer{{Name: "docs"}}},
		{Name: "contexte", Prompt: "ctx", Filters: []string{"*@exemple.com"}},
	}
	got := personas.ResolvePrompts("projet-a", matcherFor("jean@exemple.com"))
	if len(got) != 1 || got[0] != "ctx" {
		t.Fatalf("ResolvePrompts = %v, attendu [ctx]", got)
	}
}

func TestMergeMCPServers(t *testing.T) {
	base := []MCPServer{{Name: "docs", URL: "agent"}, {Name: "shell"}}
	extra := []MCPServer{{Name: "docs", URL: "persona"}, {Name: "admin"}, {Name: "admin"}}

	got := MergeMCPServers(base, extra)

	want := []string{"docs", "shell", "admin"}
	if len(got) != len(want) {
		t.Fatalf("MergeMCPServers = %v, attendu %v", got, want)
	}
	for i := range got {
		if got[i].Name != want[i] {
			t.Fatalf("MergeMCPServers[%d] = %q, attendu %q", i, got[i].Name, want[i])
		}
	}
	// L'agent fait autorité sur un serveur homonyme.
	if got[0].URL != "agent" {
		t.Fatalf("serveur homonyme: URL = %q, attendu celle de l'agent", got[0].URL)
	}
	// base ne doit pas avoir été mutée.
	if len(base) != 2 {
		t.Fatalf("base mutée: %v", base)
	}
}

func TestAugmentSystemPrompt(t *testing.T) {
	if got := AugmentSystemPrompt("base", nil); got != "base" {
		t.Fatalf("sans persona, prompt attendu inchangé, obtenu %q", got)
	}

	got := AugmentSystemPrompt("base", []string{"desc-a", "desc-b"})
	if !strings.HasPrefix(got, "base\n\n") {
		t.Fatalf("le prompt de base doit précéder le contexte, obtenu %q", got)
	}
	for _, want := range []string{"# Contexte sur l'utilisateur", "desc-a", "desc-b"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt augmenté ne contient pas %q: %q", want, got)
		}
	}

	// Prompt de base vide : pas de saut de ligne parasite en tête.
	if got := AugmentSystemPrompt("", []string{"desc"}); strings.HasPrefix(got, "\n") {
		t.Fatalf("prompt de base vide ne doit pas produire de saut de ligne en tête: %q", got)
	}
}
