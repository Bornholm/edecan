package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"golang.org/x/oauth2"
)

// githubAPIVersion fixe la version de l'API REST GitHub utilisée
// (https://docs.github.com/en/rest/about-the-rest-api/api-versions).
const githubAPIVersion = "2022-11-28"

// githubUser est la projection partielle de la réponse de GET /user
// (https://docs.github.com/en/rest/users/users#get-the-authenticated-user).
// Email est souvent vide si l'utilisateur n'a pas rendu son adresse
// publique — il faut alors se replier sur GET /user/emails.
type githubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type githubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

// exchangeGitHub résout l'identité GitHub à partir d'un jeton d'accès OAuth2
// — GitHub n'étant pas un fournisseur OIDC, il n'y a pas d'id_token à
// vérifier : l'identité est lue depuis l'API REST github.com/user
// (cf. IdPConfig, package doc).
func (idp *idP) exchangeGitHub(ctx context.Context, token *oauth2.Token) (*Identity, error) {
	client := idp.oauth2.Client(ctx, token)

	var user githubUser
	if err := githubGet(ctx, client, "https://api.github.com/user", &user); err != nil {
		return nil, fmt.Errorf("lecture du profil github: %w", err)
	}

	email := user.Email
	if email == "" {
		var emails []githubEmail
		if err := githubGet(ctx, client, "https://api.github.com/user/emails", &emails); err != nil {
			return nil, fmt.Errorf("lecture des adresses email github: %w", err)
		}
		for _, e := range emails {
			if e.Primary && e.Verified {
				email = e.Email
				break
			}
		}
	}
	if email == "" {
		return nil, fmt.Errorf("github: aucune adresse email vérifiée disponible pour %q (scope user:email requis)", user.Login)
	}

	displayName := user.Name
	if displayName == "" {
		displayName = user.Login
	}

	return &Identity{
		IdPName:     idp.name,
		Subject:     strconv.FormatInt(user.ID, 10),
		Email:       email,
		DisplayName: displayName,
	}, nil
}

// githubGet exécute un GET authentifié (client porte déjà le jeton OAuth2)
// vers l'API REST GitHub et décode la réponse JSON dans out.
func githubGet(ctx context.Context, client *http.Client, reqURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("construction de la requête github: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("appel de l'API github (%s): %w", reqURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("réponse github %d (%s)", resp.StatusCode, reqURL)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("décodage de la réponse github: %w", err)
	}
	return nil
}
