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
