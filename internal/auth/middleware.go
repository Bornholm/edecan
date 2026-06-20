package auth

import (
	"context"
	"net/http"

	"edecan/internal/core/model"
)

type contextKey string

const identityContextKey contextKey = "edecan.identity"

// WithIdentity attache l'identité authentifiée au contexte de requête.
func WithIdentity(ctx context.Context, u *model.User) context.Context {
	return context.WithValue(ctx, identityContextKey, u)
}

// IdentityFromContext retourne l'utilisateur authentifié, ou nil si la
// requête n'est pas authentifiée.
func IdentityFromContext(ctx context.Context) *model.User {
	u, _ := ctx.Value(identityContextKey).(*model.User)
	return u
}

// SessionStore résout l'utilisateur associé à la requête courante (cookie de
// session serveur : HttpOnly, Secure, SameSite=Lax — PLAN.md §Phase 6). Son
// implémentation concrète (signature/chiffrement du cookie) reste à câbler
// avec la librairie de session retenue.
type SessionStore interface {
	UserFromRequest(r *http.Request) (*model.User, error)
}

// RequireAuth est le middleware garantissant qu'aucune ressource n'est
// accessible sans session valide (SPEC §Sécurité). L'autorisation par
// projet/rôle reste de la responsabilité de la couche service
// (cf. PLAN.md §Phase 1, points d'attention sécurité).
func RequireAuth(store SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := store.UserFromRequest(r)
			if err != nil || user == nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			ctx := WithIdentity(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
