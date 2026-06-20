package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

const (
	sessionCookieName = "edecan_session"
	stateCookieName   = "edecan_oauth_state"
	sessionTTL        = 30 * 24 * time.Hour
)

// CookieSessionStore implémente SessionStore via un cookie signé HMAC
// (HttpOnly, SameSite=Lax, Secure si secure=true — SPEC §Sécurité). Le cookie
// ne contient que l'ID utilisateur et une expiration ; l'identité complète
// est rechargée depuis UserRepository à chaque requête.
type CookieSessionStore struct {
	secret []byte
	users  port.UserRepository
	secure bool
}

// NewCookieSessionStore construit un CookieSessionStore. secure DOIT être
// true en production (déploiement HTTPS).
func NewCookieSessionStore(secret []byte, users port.UserRepository, secure bool) *CookieSessionStore {
	return &CookieSessionStore{secret: secret, users: users, secure: secure}
}

var _ SessionStore = (*CookieSessionStore)(nil)

// IssueSession pose le cookie de session pour userID.
func (s *CookieSessionStore) IssueSession(w http.ResponseWriter, userID model.UserID) {
	expires := time.Now().Add(sessionTTL)
	payload := fmt.Sprintf("%d.%d", userID, expires.Unix())
	value := payload + "." + s.sign(payload)

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    base64.RawURLEncoding.EncodeToString([]byte(value)),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	})
}

// ClearSession supprime le cookie de session (déconnexion).
func (s *CookieSessionStore) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// UserFromRequest résout l'utilisateur authentifié depuis le cookie de
// session, ou (nil, nil) si absent/invalide/expiré — RequireAuth redirige
// alors vers /login.
func (s *CookieSessionStore) UserFromRequest(r *http.Request) (*model.User, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, nil
	}

	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return nil, nil
	}

	parts := strings.SplitN(string(raw), ".", 3)
	if len(parts) != 3 {
		return nil, nil
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(s.sign(payload)), []byte(parts[2])) {
		return nil, nil
	}

	userID, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return nil, nil
	}
	expiresUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > expiresUnix {
		return nil, nil
	}

	user, err := s.users.FindByID(r.Context(), model.UserID(userID))
	if err != nil {
		return nil, fmt.Errorf("résolution de l'utilisateur de session: %w", err)
	}
	return user, nil
}

func (s *CookieSessionStore) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// SetOAuthState pose un cookie d'état CSRF court (10 min) avant la
// redirection vers l'IdP, et retourne la valeur à transmettre en paramètre
// `state` (cf. PLAN.md §Phase 6).
func SetOAuthState(w http.ResponseWriter, secure bool) (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("génération de l'état CSRF: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(buf)

	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	return state, nil
}

// ErrInvalidOAuthState signale un paramètre `state` absent, expiré ou ne
// correspondant pas au cookie posé par SetOAuthState (protection CSRF).
var ErrInvalidOAuthState = errors.New("état OAuth invalide ou expiré")

// ConsumeOAuthState vérifie que gotState correspond au cookie posé par
// SetOAuthState, puis l'invalide (usage unique).
func ConsumeOAuthState(r *http.Request, w http.ResponseWriter, secure bool, gotState string) error {
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	cookie, err := r.Cookie(stateCookieName)
	if err != nil || cookie.Value == "" || gotState == "" {
		return ErrInvalidOAuthState
	}
	if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(gotState)) != 1 {
		return ErrInvalidOAuthState
	}
	return nil
}
