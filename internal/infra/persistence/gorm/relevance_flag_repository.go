package gorm

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

type relevanceFlagRepository struct{ db *gorm.DB }

// NewRelevanceFlagRepository construit un port.RelevanceFlagRepository basé
// sur GORM/SQLite.
func NewRelevanceFlagRepository(db *gorm.DB) port.RelevanceFlagRepository {
	return &relevanceFlagRepository{db: db}
}

func (r *relevanceFlagRepository) Save(ctx context.Context, f *model.RelevanceFlag) error {
	row := RelevanceFlagRow{
		ID:        uint(f.ID),
		SessionID: uint(f.SessionID),
		UserID:    uint(f.UserID),
		FlaggedAt: f.FlaggedAt,
	}
	if err := r.db.WithContext(ctx).Save(&row).Error; err != nil {
		return fmt.Errorf("sauvegarde relevance flag: %w", err)
	}
	f.ID = model.RelevanceFlagID(row.ID)
	return nil
}

func (r *relevanceFlagRepository) ExistsForSession(ctx context.Context, sessionID model.SessionID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&RelevanceFlagRow{}).
		Where("session_id = ?", uint(sessionID)).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("vérification relevance flag: %w", err)
	}
	return count > 0, nil
}

// DeleteBySession supprime le signalement éventuel de sessionID — utilisé
// lors de la suppression d'une session (cf. SessionRepository.Delete).
func (r *relevanceFlagRepository) DeleteBySession(ctx context.Context, sessionID model.SessionID) error {
	if err := r.db.WithContext(ctx).Where("session_id = ?", uint(sessionID)).Delete(&RelevanceFlagRow{}).Error; err != nil {
		return fmt.Errorf("suppression du signalement de la session %d: %w", sessionID, err)
	}
	return nil
}
