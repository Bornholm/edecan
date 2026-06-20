package gorm

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

type messageRepository struct{ db *gorm.DB }

// NewMessageRepository construit un port.MessageRepository basé sur GORM/SQLite.
func NewMessageRepository(db *gorm.DB) port.MessageRepository {
	return &messageRepository{db: db}
}

func (r *messageRepository) Save(ctx context.Context, m *model.Message) error {
	row := MessageRow{
		ID:        uint(m.ID),
		SessionID: uint(m.SessionID),
		Role:      string(m.Role),
		Content:   m.Content,
		CreatedAt: m.CreatedAt,
	}
	if err := r.db.WithContext(ctx).Save(&row).Error; err != nil {
		return fmt.Errorf("sauvegarde message: %w", err)
	}
	m.ID = model.MessageID(row.ID)
	return nil
}

// ListBySession retourne les messages d'une session, triés chronologiquement.
// Le cloisonnement par session — donc indirectement par utilisateur via
// SessionRepository — est de la responsabilité de l'appelant (couche
// service), qui doit vérifier la propriété de la session avant d'appeler
// cette méthode.
func (r *messageRepository) ListBySession(ctx context.Context, sessionID model.SessionID) ([]*model.Message, error) {
	var rows []MessageRow
	err := r.db.WithContext(ctx).
		Where("session_id = ?", uint(sessionID)).
		Order("created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listing messages: %w", err)
	}

	messages := make([]*model.Message, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, &model.Message{
			ID:        model.MessageID(row.ID),
			SessionID: model.SessionID(row.SessionID),
			Role:      model.MessageRole(row.Role),
			Content:   row.Content,
			CreatedAt: row.CreatedAt,
		})
	}
	return messages, nil
}

// DeleteBySession supprime tous les messages de sessionID — utilisé lors de
// la suppression d'une session (cf. SessionRepository.Delete). Le
// cloisonnement par utilisateur est de la responsabilité de l'appelant.
func (r *messageRepository) DeleteBySession(ctx context.Context, sessionID model.SessionID) error {
	if err := r.db.WithContext(ctx).Where("session_id = ?", uint(sessionID)).Delete(&MessageRow{}).Error; err != nil {
		return fmt.Errorf("suppression des messages de la session %d: %w", sessionID, err)
	}
	return nil
}
