// Package auth implémente l'authentification OIDC multi-IdP d'edecán
// (SPEC §Authentification & Autorisation, point 1) et l'autorisation par
// projet/rôle (PLAN.md §Phase 6).
package auth

import (
	"context"
	"fmt"
	"sort"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	githuboauth "golang.org/x/oauth2/github"
)

// Identity est l'identité résolue après un échange OIDC réussi.
type Identity struct {
	IdPName     string
	Subject     string
	Email       string
	DisplayName string
}

// IdPConfig regroupe les paramètres nécessaires à l'enregistrement d'un
// fournisseur d'identité auprès du Manager. Type vaut "oidc" (par défaut,
// IdP avec découverte ".well-known/openid-configuration" — Keycloak, Gitea,
// etc.) ou "oauth2" — pour un fournisseur sans OIDC (aucune découverte, pas
// d'id_token), où l'identité est résolue via l'API REST du fournisseur après
// l'échange OAuth2 "classique" (cf. github.go ; seul GitHub est implémenté
// pour l'instant derrière ce type).
type IdPConfig struct {
	Name         string
	Type         string // "oidc" (défaut) | "oauth2"
	Issuer       string // ignoré pour Type "oauth2"
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// idPKind distingue les deux familles d'IdP supportées — leur traitement à
// l'échange du code d'autorisation diffère totalement (id_token vérifié
// cryptographiquement pour OIDC, appel à l'API REST du fournisseur pour
// OAuth2 "classique").
type idPKind int

const (
	idPKindOIDC idPKind = iota
	idPKindOAuth2
)

// idP est un fournisseur d'identité enregistré, prêt pour le flux
// authorization code.
type idP struct {
	name     string
	kind     idPKind
	provider *oidc.Provider        // nil pour kind == idPKindGitHub
	verifier *oidc.IDTokenVerifier // nil pour kind == idPKindGitHub
	oauth2   oauth2.Config
}

// Manager résout l'identité d'un utilisateur à travers plusieurs IdP
// configurables (SPEC §Authentification, point 1).
type Manager struct {
	idps map[string]*idP
}

// NewManager effectue la découverte OIDC (".well-known/openid-configuration")
// pour chaque IdP de type "oidc", et enregistre directement les IdP de type
// "github" (pas de découverte possible, cf. IdPConfig). Échoue fail-fast si
// un IdP OIDC est inaccessible au démarrage — edecán ne doit jamais démarrer
// avec une configuration d'authentification incomplète.
func NewManager(ctx context.Context, configs []IdPConfig) (*Manager, error) {
	m := &Manager{idps: make(map[string]*idP, len(configs))}

	for _, c := range configs {
		switch c.Type {
		case "", "oidc":
			provider, err := oidc.NewProvider(ctx, c.Issuer)
			if err != nil {
				return nil, fmt.Errorf("découverte OIDC pour l'IdP %q (%s): %w", c.Name, c.Issuer, err)
			}
			m.idps[c.Name] = &idP{
				name:     c.Name,
				kind:     idPKindOIDC,
				provider: provider,
				verifier: provider.Verifier(&oidc.Config{ClientID: c.ClientID}),
				oauth2: oauth2.Config{
					ClientID:     c.ClientID,
					ClientSecret: c.ClientSecret,
					RedirectURL:  c.RedirectURL,
					Endpoint:     provider.Endpoint(),
					Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
				},
			}
		case "oauth2":
			// Seul GitHub est implémenté derrière ce type pour l'instant —
			// cf. IdPConfig.
			m.idps[c.Name] = &idP{
				name: c.Name,
				kind: idPKindOAuth2,
				oauth2: oauth2.Config{
					ClientID:     c.ClientID,
					ClientSecret: c.ClientSecret,
					RedirectURL:  c.RedirectURL,
					Endpoint:     githuboauth.Endpoint,
					Scopes:       []string{"read:user", "user:email"},
				},
			}
		default:
			return nil, fmt.Errorf("IdP %q: type %q inconnu (oidc|oauth2)", c.Name, c.Type)
		}
	}

	return m, nil
}

// IdPNames retourne les noms des IdP enregistrés, pour l'affichage de la
// page de connexion (SPEC §Authentification, point 1 : plusieurs IdP).
func (m *Manager) IdPNames() []string {
	names := make([]string, 0, len(m.idps))
	for name := range m.idps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// AuthCodeURL retourne l'URL de redirection vers l'IdP nommé idpName, avec
// l'état CSRF state (à générer et stocker côté serveur par l'appelant).
func (m *Manager) AuthCodeURL(idpName, state string) (string, error) {
	idp, ok := m.idps[idpName]
	if !ok {
		return "", fmt.Errorf("IdP %q inconnu", idpName)
	}
	return idp.oauth2.AuthCodeURL(state), nil
}

// Exchange échange le code d'autorisation contre un jeton, vérifie l'ID
// token et retourne l'identité résolue.
func (m *Manager) Exchange(ctx context.Context, idpName, code string) (*Identity, error) {
	idp, ok := m.idps[idpName]
	if !ok {
		return nil, fmt.Errorf("IdP %q inconnu", idpName)
	}

	token, err := idp.oauth2.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("échange du code d'autorisation auprès de %q: %w", idpName, err)
	}

	if idp.kind == idPKindOAuth2 {
		return idp.exchangeGitHub(ctx, token)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("réponse de %q sans id_token", idpName)
	}

	idToken, err := idp.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("vérification de l'id_token de %q: %w", idpName, err)
	}

	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("lecture des claims de %q: %w", idpName, err)
	}

	return &Identity{
		IdPName:     idpName,
		Subject:     idToken.Subject,
		Email:       claims.Email,
		DisplayName: claims.Name,
	}, nil
}
