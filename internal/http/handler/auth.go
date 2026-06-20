package handler

import (
	"net/http"

	"edecan/internal/auth"
	"edecan/internal/http/view/page"
)

// LoginPage affiche le choix de fournisseur d'identité OIDC
// (SPEC §Authentification, point 1).
func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	errMsg := ""
	if e := r.URL.Query().Get("error"); e != "" {
		errMsg = "Connexion impossible, veuillez réessayer."
	}
	render(w, r, page.Login(h.Registry.AuthManager.IdPNames(), errMsg))
}

// StartAuth redirige vers l'IdP demandé, avec un état CSRF posé en cookie
// (PLAN.md §Phase 6).
func (h *Handlers) StartAuth(w http.ResponseWriter, r *http.Request) {
	idpName := r.PathValue("idp")

	state, err := auth.SetOAuthState(w, h.Secure)
	if err != nil {
		h.Logger.ErrorContext(r.Context(), "génération de l'état OAuth", "error", err)
		http.Error(w, "erreur interne", http.StatusInternalServerError)
		return
	}

	authURL, err := h.Registry.AuthManager.AuthCodeURL(idpName, state)
	if err != nil {
		http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

// Callback échange le code d'autorisation, réconcilie l'identité locale et
// pose le cookie de session (SPEC §Authentification, point 1).
func (h *Handlers) Callback(w http.ResponseWriter, r *http.Request) {
	idpName := r.PathValue("idp")
	ctx := r.Context()

	if err := auth.ConsumeOAuthState(r, w, h.Secure, r.URL.Query().Get("state")); err != nil {
		http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
		return
	}

	identity, err := h.Registry.AuthManager.Exchange(ctx, idpName, code)
	if err != nil {
		h.Logger.ErrorContext(ctx, "échange OIDC", "idp", idpName, "error", err)
		http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
		return
	}

	user, err := h.AuthService.ResolveOrCreateUser(ctx, identity.IdPName, identity.Subject, identity.Email, identity.DisplayName)
	if err != nil {
		h.Logger.ErrorContext(ctx, "résolution de l'utilisateur", "error", err)
		http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
		return
	}

	h.SessionStore.IssueSession(w, user.ID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Logout supprime le cookie de session.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	h.SessionStore.ClearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
