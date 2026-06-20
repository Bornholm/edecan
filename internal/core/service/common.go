// Package service contient la logique métier (cas d'usage) d'edecán. Toute
// autorisation par session/projet/rôle est vérifiée ici — jamais uniquement
// côté UI (cf. PLAN.md §Phase 1, points d'attention sécurité ; SPEC §Sécurité).
package service

import (
	"context"
	"errors"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

// ErrNotFound signale une ressource absente (session, projet, ticket mappé).
var ErrNotFound = errors.New("ressource introuvable")

// ErrForbidden signale une violation du cloisonnement par utilisateur/rôle.
var ErrForbidden = errors.New("accès refusé")

// ErrSessionHasTicket signale qu'une session ayant déjà donné lieu à un
// ticket ne peut pas être supprimée — elle fait partie du fil d'audit du
// ticket (cf. ChatService.DeleteSession).
var ErrSessionHasTicket = errors.New("session liée à un ticket")

// getOwnedSession charge la session sessionID et vérifie qu'elle appartient
// à userID — le cloisonnement ne doit jamais reposer sur un filtrage
// applicatif a posteriori (cf. PLAN.md §Phase 1).
func getOwnedSession(ctx context.Context, sessions port.SessionRepository, sessionID model.SessionID, userID model.UserID) (*model.Session, error) {
	sess, err := sessions.FindByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, ErrNotFound
	}
	if sess.UserID != userID {
		return nil, ErrForbidden
	}
	return sess, nil
}
