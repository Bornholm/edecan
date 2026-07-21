package gorm

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

type shareRepository struct{ db *gorm.DB }

// NewShareRepository construit un port.ShareRepository basé sur GORM/SQLite.
func NewShareRepository(db *gorm.DB) port.ShareRepository {
	return &shareRepository{db: db}
}

func (r *shareRepository) Save(ctx context.Context, s *model.SharedConversation) error {
	row := shareToRow(*s)
	if err := r.db.WithContext(ctx).Save(&row).Error; err != nil {
		return fmt.Errorf("sauvegarde partage: %w", err)
	}
	s.ID = model.ShareID(row.ID)
	return nil
}

func (r *shareRepository) FindByToken(ctx context.Context, token model.ShareToken) (*model.SharedConversation, error) {
	var row SharedConversationRow
	err := r.db.WithContext(ctx).
		Where("token = ?", string(token)).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("lecture partage par jeton: %w", err)
	}
	s := shareFromRow(row)
	return &s, nil
}

// FindActiveBySession retourne le partage non révoqué de la session, filtré en
// SQL — (nil, nil) si aucun.
func (r *shareRepository) FindActiveBySession(ctx context.Context, sessionID model.SessionID) (*model.SharedConversation, error) {
	var row SharedConversationRow
	err := r.db.WithContext(ctx).
		Where("session_id = ? AND revoked_at IS NULL", uint(sessionID)).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("recherche partage actif de la session %d: %w", sessionID, err)
	}
	s := shareFromRow(row)
	return &s, nil
}

func shareFromRow(row SharedConversationRow) model.SharedConversation {
	return model.SharedConversation{
		ID:        model.ShareID(row.ID),
		Token:     model.ShareToken(row.Token),
		SessionID: model.SessionID(row.SessionID),
		CreatedBy: model.UserID(row.CreatedBy),
		SharedAt:  row.SharedAt,
		RevokedAt: row.RevokedAt,
	}
}

func shareToRow(s model.SharedConversation) SharedConversationRow {
	return SharedConversationRow{
		ID:        uint(s.ID),
		Token:     string(s.Token),
		SessionID: uint(s.SessionID),
		CreatedBy: uint(s.CreatedBy),
		SharedAt:  s.SharedAt,
		RevokedAt: s.RevokedAt,
	}
}
