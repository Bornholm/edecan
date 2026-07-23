package gorm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

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
		ID:         uint(m.ID),
		SessionID:  uint(m.SessionID),
		Role:       string(m.Role),
		Content:    m.Content,
		Reasoning:  m.Reasoning,
		ToolCalls:  encodeToolCalls(ctx, m.ToolCalls),
		ToolCallID: m.ToolCallID,
		ToolName:   m.ToolName,
		CreatedAt:  m.CreatedAt,
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
//
// Le tri départage les ex æquo par identifiant : les messages d'un tour
// d'outils sont écrits d'affilée et peuvent partager le même horodatage, alors
// que leur ordre relatif (appels puis résultats) doit être préservé — sans quoi
// le contexte reconstruit pour le LLM serait dépareillé.
func (r *messageRepository) ListBySession(ctx context.Context, sessionID model.SessionID) ([]*model.Message, error) {
	var rows []MessageRow
	err := r.db.WithContext(ctx).
		Where("session_id = ?", uint(sessionID)).
		Order("created_at ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listing messages: %w", err)
	}

	messages := make([]*model.Message, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, &model.Message{
			ID:         model.MessageID(row.ID),
			SessionID:  model.SessionID(row.SessionID),
			Role:       model.MessageRole(row.Role),
			Content:    row.Content,
			Reasoning:  row.Reasoning,
			ToolCalls:  decodeToolCalls(ctx, row.ToolCalls),
			ToolCallID: row.ToolCallID,
			ToolName:   row.ToolName,
			CreatedAt:  row.CreatedAt,
		})
	}
	return messages, nil
}

// encodeToolCalls sérialise les appels d'outils d'un message. Un échec de
// sérialisation est journalisé sans faire échouer l'écriture : perdre la trace
// d'un appel d'outil dégrade la mémoire de l'agent, perdre le message lui-même
// casserait la conversation.
func encodeToolCalls(ctx context.Context, calls []model.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	raw, err := json.Marshal(calls)
	if err != nil {
		slog.ErrorContext(ctx, "sérialisation des appels d'outils d'un message", "error", err)
		return ""
	}
	return string(raw)
}

// decodeToolCalls relit les appels d'outils sérialisés. Une valeur illisible
// est traitée comme absente : le tour d'outil correspondant sera simplement
// écarté du contexte reconstruit (cf. internal/infra/llm.toLLMMessages).
func decodeToolCalls(ctx context.Context, raw string) []model.ToolCall {
	if raw == "" {
		return nil
	}
	var calls []model.ToolCall
	if err := json.Unmarshal([]byte(raw), &calls); err != nil {
		slog.ErrorContext(ctx, "lecture des appels d'outils d'un message", "error", err)
		return nil
	}
	return calls
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
