package config

import (
	"strings"
	"testing"
)

// baseValidConfig retourne une configuration minimale valide, à muter dans les
// tests de validation des personas.
func baseValidConfig() *Config {
	return &Config{
		Server: ServerConfig{Addr: ":8080", SQLitePath: "edecan.db"},
		IdPs:   []IdPConfig{{Name: "idp", Issuer: "https://idp.exemple.com"}},
		Agents: []AgentConfig{{Name: "agent", Provider: "openai", Model: "gpt", APIKey: "clef"}},
		Projects: []ProjectConfig{{
			Slug:       "projet-a",
			Name:       "Projet A",
			Agent:      "agent",
			Membership: []MembershipRule{{Pattern: "*@exemple.com", Role: RoleUser}},
		}},
	}
}

func TestValidatePersonas(t *testing.T) {
	cases := []struct {
		name    string
		persona PersonaConfig
		wantErr string // sous-chaîne attendue ; vide ⇒ pas d'erreur
	}{
		{
			name:    "persona globale valide",
			persona: PersonaConfig{Name: "p", Prompt: "ctx", Filters: []string{"*@exemple.com"}},
		},
		{
			name:    "persona ciblant un projet existant",
			persona: PersonaConfig{Name: "p", Prompt: "ctx", Filters: []string{"*@exemple.com"}, Projects: []string{"projet-a"}},
		},
		{
			name:    "sans nom",
			persona: PersonaConfig{Prompt: "ctx", Filters: []string{"*@exemple.com"}},
			wantErr: "sans nom",
		},
		{
			name:    "sans prompt",
			persona: PersonaConfig{Name: "p", Filters: []string{"*@exemple.com"}},
			wantErr: "prompt requis",
		},
		{
			name:    "sans filtre",
			persona: PersonaConfig{Name: "p", Prompt: "ctx"},
			wantErr: "au moins un filtre",
		},
		{
			name:    "filtre malformé",
			persona: PersonaConfig{Name: "p", Prompt: "ctx", Filters: []string{"pas-un-email"}},
			wantErr: "malformé",
		},
		{
			name:    "projet inexistant",
			persona: PersonaConfig{Name: "p", Prompt: "ctx", Filters: []string{"*@exemple.com"}, Projects: []string{"inconnu"}},
			wantErr: "introuvable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseValidConfig()
			cfg.Personas = []PersonaConfig{tc.persona}
			err := cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, attendu nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, attendu une erreur contenant %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidatePersonasNomDuplique(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Personas = []PersonaConfig{
		{Name: "dup", Prompt: "a", Filters: []string{"*@exemple.com"}},
		{Name: "dup", Prompt: "b", Filters: []string{"*@exemple.com"}},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "dupliqué") {
		t.Fatalf("Validate() = %v, attendu une erreur de nom dupliqué", err)
	}
}
