package gorm

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

type ticketMappingRepository struct{ db *gorm.DB }

// NewTicketMappingRepository construit un port.TicketMappingRepository basé
// sur GORM/SQLite.
func NewTicketMappingRepository(db *gorm.DB) port.TicketMappingRepository {
	return &ticketMappingRepository{db: db}
}

func (r *ticketMappingRepository) Save(ctx context.Context, m *model.TicketMapping) error {
	row := ticketMappingToRow(*m)
	if err := r.db.WithContext(ctx).Save(&row).Error; err != nil {
		return fmt.Errorf("sauvegarde ticket mapping: %w", err)
	}
	m.ID = model.TicketMappingID(row.ID)
	return nil
}

func (r *ticketMappingRepository) FindByRef(ctx context.Context, backendID model.TicketBackendID, ref model.TicketRef) (*model.TicketMapping, error) {
	var row TicketMappingRow
	err := r.db.WithContext(ctx).
		Where("ticket_backend_id = ? AND ref = ?", string(backendID), string(ref)).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("lecture ticket mapping: %w", err)
	}
	m := ticketMappingFromRow(row)
	return &m, nil
}

func (r *ticketMappingRepository) ListByProjectAndUser(ctx context.Context, p model.ProjectID, u model.UserID) ([]*model.TicketMapping, error) {
	var rows []TicketMappingRow
	err := r.db.WithContext(ctx).
		Where("project_id = ? AND requester_id = ?", string(p), uint(u)).
		Order("created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listing ticket mappings par user: %w", err)
	}
	return ticketMappingsFromRows(rows), nil
}

func (r *ticketMappingRepository) ListByProject(ctx context.Context, p model.ProjectID) ([]*model.TicketMapping, error) {
	var rows []TicketMappingRow
	err := r.db.WithContext(ctx).
		Where("project_id = ?", string(p)).
		Order("created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listing ticket mappings par projet: %w", err)
	}
	return ticketMappingsFromRows(rows), nil
}

func ticketMappingsFromRows(rows []TicketMappingRow) []*model.TicketMapping {
	mappings := make([]*model.TicketMapping, 0, len(rows))
	for _, row := range rows {
		m := ticketMappingFromRow(row)
		mappings = append(mappings, &m)
	}
	return mappings
}

func ticketMappingFromRow(row TicketMappingRow) model.TicketMapping {
	var sessionID *model.SessionID
	if row.SessionID != nil {
		v := model.SessionID(*row.SessionID)
		sessionID = &v
	}
	return model.TicketMapping{
		ID:              model.TicketMappingID(row.ID),
		ProjectID:       model.ProjectID(row.ProjectID),
		TicketBackendID: model.TicketBackendID(row.TicketBackendID),
		Ref:             model.TicketRef(row.Ref),
		RequesterID:     model.UserID(row.RequesterID),
		SessionID:       sessionID,
		CreatedAt:       row.CreatedAt,
	}
}

func ticketMappingToRow(m model.TicketMapping) TicketMappingRow {
	var sessionID *uint
	if m.SessionID != nil {
		v := uint(*m.SessionID)
		sessionID = &v
	}
	return TicketMappingRow{
		ID:              uint(m.ID),
		ProjectID:       string(m.ProjectID),
		TicketBackendID: string(m.TicketBackendID),
		Ref:             string(m.Ref),
		RequesterID:     uint(m.RequesterID),
		SessionID:       sessionID,
		CreatedAt:       m.CreatedAt,
	}
}
