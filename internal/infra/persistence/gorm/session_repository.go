package gorm

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

type sessionRepository struct{ db *gorm.DB }

// NewSessionRepository construit un port.SessionRepository basé sur GORM/SQLite.
func NewSessionRepository(db *gorm.DB) port.SessionRepository {
	return &sessionRepository{db: db}
}

func (r *sessionRepository) Save(ctx context.Context, s *model.Session) error {
	row := sessionToRow(*s)
	if err := r.db.WithContext(ctx).Save(&row).Error; err != nil {
		return fmt.Errorf("sauvegarde session: %w", err)
	}
	s.ID = model.SessionID(row.ID)
	return nil
}

func (r *sessionRepository) FindByID(ctx context.Context, id model.SessionID) (*model.Session, error) {
	var row SessionRow
	if err := r.db.WithContext(ctx).First(&row, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("lecture session %d: %w", id, err)
	}
	s := sessionFromRow(row)
	return &s, nil
}

// ListByUserAndProject filtre en SQL — le cloisonnement par utilisateur ne
// doit jamais reposer sur un filtrage applicatif a posteriori
// (cf. PLAN.md §Phase 1).
func (r *sessionRepository) ListByUserAndProject(ctx context.Context, u model.UserID, p model.ProjectID) ([]*model.Session, error) {
	var rows []SessionRow
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND project_id = ?", uint(u), string(p)).
		Order("updated_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	sessions := make([]*model.Session, 0, len(rows))
	for _, row := range rows {
		s := sessionFromRow(row)
		sessions = append(sessions, &s)
	}
	return sessions, nil
}

// FindMostRecentByUser filtre en SQL, tous projets confondus — utilisé pour
// reprendre l'utilisateur sur son dernier projet actif à la connexion.
func (r *sessionRepository) FindMostRecentByUser(ctx context.Context, u model.UserID) (*model.Session, error) {
	var row SessionRow
	err := r.db.WithContext(ctx).
		Where("user_id = ?", uint(u)).
		Order("updated_at DESC").
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("recherche de la session la plus récente: %w", err)
	}
	s := sessionFromRow(row)
	return &s, nil
}

// Delete supprime définitivement la session — l'appelant (couche service)
// DOIT avoir vérifié la propriété au préalable.
func (r *sessionRepository) Delete(ctx context.Context, id model.SessionID) error {
	if err := r.db.WithContext(ctx).Delete(&SessionRow{}, uint(id)).Error; err != nil {
		return fmt.Errorf("suppression session %d: %w", id, err)
	}
	return nil
}

func sessionFromRow(row SessionRow) model.Session {
	var ref *model.TicketRef
	if row.ConvertedTicketRef != nil {
		v := model.TicketRef(*row.ConvertedTicketRef)
		ref = &v
	}
	return model.Session{
		ID:                 model.SessionID(row.ID),
		ProjectID:          model.ProjectID(row.ProjectID),
		UserID:             model.UserID(row.UserID),
		Title:              row.Title,
		Status:             model.SessionStatus(row.Status),
		ConvertedTicketRef: ref,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}

func sessionToRow(s model.Session) SessionRow {
	var ref *string
	if s.ConvertedTicketRef != nil {
		v := string(*s.ConvertedTicketRef)
		ref = &v
	}
	return SessionRow{
		ID:                 uint(s.ID),
		ProjectID:          string(s.ProjectID),
		UserID:             uint(s.UserID),
		Title:              s.Title,
		Status:             string(s.Status),
		ConvertedTicketRef: ref,
		CreatedAt:          s.CreatedAt,
		UpdatedAt:          s.UpdatedAt,
	}
}
