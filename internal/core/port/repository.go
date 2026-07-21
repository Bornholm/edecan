package port

import (
	"context"

	"edecan/internal/core/model"
)

// UserRepository persiste le cache local d'identité OIDC.
type UserRepository interface {
	FindByID(ctx context.Context, id model.UserID) (*model.User, error)
	// FindByOIDCSubject résout un utilisateur par IdP + subject — clé stable
	// recommandée pour la réconciliation d'identité multi-IdP
	// (SPEC §Open Questions, point 4).
	FindByOIDCSubject(ctx context.Context, idpName, subject string) (*model.User, error)
	Save(ctx context.Context, u *model.User) error
}

// SessionRepository persiste les sessions de chat. Toute méthode de liste ou
// de lecture DOIT filtrer par utilisateur au niveau de la requête — le
// cloisonnement ne doit jamais reposer sur un filtrage applicatif a
// posteriori (cf. PLAN.md §Phase 1, points d'attention sécurité).
type SessionRepository interface {
	Save(ctx context.Context, s *model.Session) error
	FindByID(ctx context.Context, id model.SessionID) (*model.Session, error)
	ListByUserAndProject(ctx context.Context, u model.UserID, p model.ProjectID) ([]*model.Session, error)
	// FindMostRecentByUser retourne la session la plus récemment mise à jour
	// de u, tous projets confondus — utilisé pour reprendre l'utilisateur sur
	// son dernier projet actif à la connexion. Retourne (nil, nil) si
	// l'utilisateur n'a encore aucune session.
	FindMostRecentByUser(ctx context.Context, u model.UserID) (*model.Session, error)
	// Delete supprime définitivement la session. L'appelant (couche service)
	// DOIT avoir vérifié la propriété au préalable.
	Delete(ctx context.Context, id model.SessionID) error
}

// MessageRepository persiste les messages d'une session.
type MessageRepository interface {
	Save(ctx context.Context, m *model.Message) error
	ListBySession(ctx context.Context, sessionID model.SessionID) ([]*model.Message, error)
	// DeleteBySession supprime tous les messages de sessionID — utilisé lors
	// de la suppression d'une session (cf. SessionRepository.Delete).
	DeleteBySession(ctx context.Context, sessionID model.SessionID) error
}

// TicketMappingRepository persiste l'autorité de correspondance
// session ↔ issue ↔ user. Contrainte d'unicité : (backend_id, ref).
type TicketMappingRepository interface {
	Save(ctx context.Context, m *model.TicketMapping) error
	FindByRef(ctx context.Context, backendID model.TicketBackendID, ref model.TicketRef) (*model.TicketMapping, error)
	// ListByProjectAndUser retourne les mappings visibles par u dans le
	// projet p. Un Support voit tous les mappings du projet, un User
	// uniquement les siens (SPEC §Tickets, point 28) — ce filtrage est
	// appliqué par l'appelant (couche service), pas par cette interface.
	ListByProjectAndUser(ctx context.Context, p model.ProjectID, u model.UserID) ([]*model.TicketMapping, error)
	ListByProject(ctx context.Context, p model.ProjectID) ([]*model.TicketMapping, error)
}

// ShareRepository persiste les partages publics de conversation. La résolution
// par jeton (FindByToken) est la seule voie d'accès de la route publique — elle
// ne filtre pas par utilisateur (partage anonyme), mais la couche service
// refuse les partages révoqués et borne l'instantané. La création et la
// révocation, elles, passent par un contrôle de propriété de session côté
// service (jamais dans ce repository).
type ShareRepository interface {
	Save(ctx context.Context, s *model.SharedConversation) error
	FindByToken(ctx context.Context, token model.ShareToken) (*model.SharedConversation, error)
	// FindActiveBySession retourne le partage actif (non révoqué) de la session,
	// ou (nil, nil) s'il n'en existe aucun — permet de réutiliser un lien
	// existant plutôt que d'en multiplier (idempotence côté service).
	FindActiveBySession(ctx context.Context, sessionID model.SessionID) (*model.SharedConversation, error)
}

// RelevanceFlagRepository persiste les signalements manuels « Cet échange
// m'a aidé » (SPEC §FAQ).
type RelevanceFlagRepository interface {
	Save(ctx context.Context, f *model.RelevanceFlag) error
	ExistsForSession(ctx context.Context, sessionID model.SessionID) (bool, error)
	// DeleteBySession supprime le signalement éventuel de sessionID — utilisé
	// lors de la suppression d'une session (cf. SessionRepository.Delete).
	DeleteBySession(ctx context.Context, sessionID model.SessionID) error
}
