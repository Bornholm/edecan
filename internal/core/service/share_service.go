package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

// shareTokenBytes est la taille de l'entropie du jeton de partage (≥ 128 bits,
// encodés en base64url) — cryptographiquement aléatoire, jamais dérivé de
// l'identifiant de session (PLAN-UX-CHAT §Phase 4, points sécurité).
const shareTokenBytes = 32

// ShareService porte les cas d'usage du partage public d'une conversation
// (PLAN-UX-CHAT §Phase 4). L'autorisation (propriété de session) est vérifiée
// ici pour la création et la révocation ; la consultation publique n'exige
// aucune authentification mais ne révèle qu'un instantané figé.
type ShareService struct {
	shares   port.ShareRepository
	sessions port.SessionRepository
	messages port.MessageRepository
}

func NewShareService(
	shares port.ShareRepository,
	sessions port.SessionRepository,
	messages port.MessageRepository,
) *ShareService {
	return &ShareService{shares: shares, sessions: sessions, messages: messages}
}

// CreateOrGet retourne le partage actif de la session, en le créant s'il
// n'existe pas encore — idempotent, pour ne pas multiplier les liens. Vérifie
// la propriété de session au préalable (ErrForbidden sinon).
func (s *ShareService) CreateOrGet(ctx context.Context, sessionID model.SessionID, userID model.UserID) (*model.SharedConversation, error) {
	if _, err := getOwnedSession(ctx, s.sessions, sessionID, userID); err != nil {
		return nil, err
	}

	existing, err := s.shares.FindActiveBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	token, err := newShareToken()
	if err != nil {
		return nil, err
	}
	share := &model.SharedConversation{
		Token:     token,
		SessionID: sessionID,
		CreatedBy: userID,
		SharedAt:  time.Now(),
	}
	if err := s.shares.Save(ctx, share); err != nil {
		return nil, err
	}
	return share, nil
}

// ActiveShare retourne le partage actif de la session s'il existe (nil sinon),
// après vérification de propriété — permet à l'UI d'afficher l'état courant
// sans en créer un.
func (s *ShareService) ActiveShare(ctx context.Context, sessionID model.SessionID, userID model.UserID) (*model.SharedConversation, error) {
	if _, err := getOwnedSession(ctx, s.sessions, sessionID, userID); err != nil {
		return nil, err
	}
	return s.shares.FindActiveBySession(ctx, sessionID)
}

// Revoke rend la session privée : le lien existant renvoie alors 404. Vérifie
// la propriété de session. Sans partage actif, l'opération est un no-op réussi
// (état déjà privé).
func (s *ShareService) Revoke(ctx context.Context, sessionID model.SessionID, userID model.UserID) error {
	if _, err := getOwnedSession(ctx, s.sessions, sessionID, userID); err != nil {
		return err
	}
	existing, err := s.shares.FindActiveBySession(ctx, sessionID)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	now := time.Now()
	existing.RevokedAt = &now
	return s.shares.Save(ctx, existing)
}

// PublicView résout un jeton de partage et retourne l'instantané figé de la
// conversation : la session et ses messages user/assistant antérieurs ou
// contemporains à SharedAt. Aucune authentification n'est requise (route
// publique), mais un partage inconnu ou révoqué renvoie ErrNotFound, et jamais
// les messages postérieurs au partage ni les rôles internes (system/summary).
func (s *ShareService) PublicView(ctx context.Context, token model.ShareToken) (*model.Session, []*model.Message, error) {
	share, err := s.shares.FindByToken(ctx, token)
	if err != nil {
		return nil, nil, err
	}
	if share == nil || !share.IsActive() {
		return nil, nil, ErrNotFound
	}

	sess, err := s.sessions.FindByID(ctx, share.SessionID)
	if err != nil {
		return nil, nil, err
	}
	if sess == nil {
		return nil, nil, ErrNotFound
	}

	all, err := s.messages.ListBySession(ctx, share.SessionID)
	if err != nil {
		return nil, nil, err
	}

	// Instantané figé : n'exposer que les échanges antérieurs ou contemporains
	// au partage, et uniquement les rôles conversationnels — jamais system/
	// summary ni tour d'outil (fuite de contexte interne, et les résultats
	// d'outils peuvent porter des données hors du champ du partage), jamais la
	// suite privée.
	visible := make([]*model.Message, 0, len(all))
	for _, m := range all {
		if m.Role != model.MessageRoleUser && m.Role != model.MessageRoleAssistant {
			continue
		}
		if m.IsToolTurn() {
			continue
		}
		if m.CreatedAt.After(share.SharedAt) {
			continue
		}
		visible = append(visible, m)
	}
	return sess, visible, nil
}

// newShareToken produit un jeton aléatoire base64url (sans padding), sûr pour
// une URL.
func newShareToken() (model.ShareToken, error) {
	buf := make([]byte, shareTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("génération du jeton de partage: %w", err)
	}
	return model.ShareToken(base64.RawURLEncoding.EncodeToString(buf)), nil
}
