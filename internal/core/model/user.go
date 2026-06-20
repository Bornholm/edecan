package model

import "time"

// User est un cache local d'identité OIDC (SPEC §Data Requirements).
// L'autorité de l'identité reste l'IdP ; edecán ne fait que mettre en cache
// les attributs nécessaires à l'affichage et à l'autorisation.
type User struct {
	ID          UserID
	OIDCSubject string
	IdPName     string
	Email       string
	DisplayName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
