package gorm

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

type userRepository struct{ db *gorm.DB }

// NewUserRepository construit un port.UserRepository basé sur GORM/SQLite.
func NewUserRepository(db *gorm.DB) port.UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) FindByID(ctx context.Context, id model.UserID) (*model.User, error) {
	var row UserRow
	if err := r.db.WithContext(ctx).First(&row, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("lecture user %d: %w", id, err)
	}
	u := userFromRow(row)
	return &u, nil
}

func (r *userRepository) FindByOIDCSubject(ctx context.Context, idpName, subject string) (*model.User, error) {
	var row UserRow
	err := r.db.WithContext(ctx).
		Where("idp_name = ? AND oidc_subject = ?", idpName, subject).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("lecture user par subject: %w", err)
	}
	u := userFromRow(row)
	return &u, nil
}

func (r *userRepository) Save(ctx context.Context, u *model.User) error {
	row := userToRow(*u)
	if err := r.db.WithContext(ctx).Save(&row).Error; err != nil {
		return fmt.Errorf("sauvegarde user: %w", err)
	}
	u.ID = model.UserID(row.ID)
	return nil
}

func userFromRow(row UserRow) model.User {
	return model.User{
		ID:          model.UserID(row.ID),
		OIDCSubject: row.OIDCSubject,
		IdPName:     row.IdPName,
		Email:       row.Email,
		DisplayName: row.DisplayName,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

func userToRow(u model.User) UserRow {
	return UserRow{
		ID:          uint(u.ID),
		OIDCSubject: u.OIDCSubject,
		IdPName:     u.IdPName,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
	}
}
