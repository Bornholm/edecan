package service

import (
	"context"
	"time"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

// AuthService résout/crée le cache local d'identité OIDC (SPEC §Data
// Requirements : User).
type AuthService struct {
	users port.UserRepository
}

func NewAuthService(users port.UserRepository) *AuthService {
	return &AuthService{users: users}
}

// ResolveOrCreateUser réconcilie l'identité par (idpName, subject) — clé
// stable recommandée par la SPEC plutôt que l'email seul, qui peut varier ou
// être partagé entre IdP (cf. SPEC §Open Questions, point 4). Les attributs
// affichables (email, nom) sont rafraîchis à chaque connexion.
func (s *AuthService) ResolveOrCreateUser(ctx context.Context, idpName, subject, email, displayName string) (*model.User, error) {
	u, err := s.users.FindByOIDCSubject(ctx, idpName, subject)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if u == nil {
		u = &model.User{
			OIDCSubject: subject,
			IdPName:     idpName,
			Email:       email,
			DisplayName: displayName,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	} else {
		u.Email = email
		u.DisplayName = displayName
		u.UpdatedAt = now
	}

	if err := s.users.Save(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// FindByID retourne l'utilisateur par son identifiant local — utilisé pour
// afficher le demandeur réel d'un ticket (cf. TicketMapping.RequesterID),
// distinct du compte technique sous lequel le ticket est créé côté backend
// (SPEC §Tickets, point 17).
func (s *AuthService) FindByID(ctx context.Context, id model.UserID) (*model.User, error) {
	return s.users.FindByID(ctx, id)
}
