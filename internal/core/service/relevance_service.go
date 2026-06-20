package service

import (
	"context"
	"time"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

// RelevanceService persiste les signalements manuels « Cet échange m'a aidé »
// (SPEC §FAQ). Aucun déclenchement automatique ni intrusif n'est permis.
type RelevanceService struct {
	sessions port.SessionRepository
	flags    port.RelevanceFlagRepository
}

func NewRelevanceService(sessions port.SessionRepository, flags port.RelevanceFlagRepository) *RelevanceService {
	return &RelevanceService{sessions: sessions, flags: flags}
}

// AlreadyFlagged indique si la session a déjà été signalée comme pertinente.
func (s *RelevanceService) AlreadyFlagged(ctx context.Context, sessionID model.SessionID) (bool, error) {
	return s.flags.ExistsForSession(ctx, sessionID)
}

// Flag enregistre le signalement, de façon idempotente (un second clic ne
// crée pas de doublon).
func (s *RelevanceService) Flag(ctx context.Context, sessionID model.SessionID, userID model.UserID) error {
	if _, err := getOwnedSession(ctx, s.sessions, sessionID, userID); err != nil {
		return err
	}

	exists, err := s.flags.ExistsForSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	flag := &model.RelevanceFlag{SessionID: sessionID, UserID: userID, FlaggedAt: time.Now()}
	return s.flags.Save(ctx, flag)
}
