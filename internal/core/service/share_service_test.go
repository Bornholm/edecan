package service_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"edecan/internal/core/model"
	"edecan/internal/core/service"
	gormrepo "edecan/internal/infra/persistence/gorm"
)

// newShareService monte un ShareService adossé à une base SQLite temporaire et
// y sème une session appartenant à ownerID, avec les messages fournis. Retourne
// le service et l'identifiant de la session créée.
func newShareService(t *testing.T, ownerID model.UserID, messages []*model.Message) (*service.ShareService, model.SessionID) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gormrepo.Open(dbPath)
	if err != nil {
		t.Fatalf("ouverture base de test: %v", err)
	}
	sessions := gormrepo.NewSessionRepository(db)
	msgs := gormrepo.NewMessageRepository(db)
	shares := gormrepo.NewShareRepository(db)

	ctx := context.Background()
	sess := &model.Session{
		ProjectID: "projet-test",
		UserID:    ownerID,
		Title:     "Ma session",
		Status:    model.SessionStatusActive,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := sessions.Save(ctx, sess); err != nil {
		t.Fatalf("création session: %v", err)
	}
	for _, m := range messages {
		m.SessionID = sess.ID
		if err := msgs.Save(ctx, m); err != nil {
			t.Fatalf("création message: %v", err)
		}
	}

	return service.NewShareService(shares, sessions, msgs), sess.ID
}

func TestShareCreateOrGetIdempotent(t *testing.T) {
	svc, sessionID := newShareService(t, 1, nil)
	ctx := context.Background()

	first, err := svc.CreateOrGet(ctx, sessionID, 1)
	if err != nil {
		t.Fatalf("premier partage: %v", err)
	}
	if first.Token == "" {
		t.Fatal("jeton vide")
	}
	if !first.IsActive() {
		t.Fatal("le partage créé devrait être actif")
	}

	second, err := svc.CreateOrGet(ctx, sessionID, 1)
	if err != nil {
		t.Fatalf("second partage: %v", err)
	}
	if second.Token != first.Token {
		t.Fatalf("un partage actif devrait être réutilisé: %q != %q", second.Token, first.Token)
	}
}

func TestShareTokensAreUnique(t *testing.T) {
	svcA, sessA := newShareService(t, 1, nil)
	svcB, sessB := newShareService(t, 1, nil)
	ctx := context.Background()

	a, err := svcA.CreateOrGet(ctx, sessA, 1)
	if err != nil {
		t.Fatalf("partage A: %v", err)
	}
	b, err := svcB.CreateOrGet(ctx, sessB, 1)
	if err != nil {
		t.Fatalf("partage B: %v", err)
	}
	if a.Token == b.Token {
		t.Fatal("deux partages distincts ne doivent jamais partager le même jeton")
	}
}

func TestShareCreateRefusesNonOwner(t *testing.T) {
	svc, sessionID := newShareService(t, 1, nil)
	ctx := context.Background()

	if _, err := svc.CreateOrGet(ctx, sessionID, 2); err != service.ErrForbidden {
		t.Fatalf("un non-propriétaire doit être refusé (ErrForbidden), obtenu: %v", err)
	}
}

func TestShareRevokeMakesPublicViewFail(t *testing.T) {
	svc, sessionID := newShareService(t, 1, nil)
	ctx := context.Background()

	share, err := svc.CreateOrGet(ctx, sessionID, 1)
	if err != nil {
		t.Fatalf("partage: %v", err)
	}
	if _, _, err := svc.PublicView(ctx, share.Token); err != nil {
		t.Fatalf("la vue publique devrait fonctionner avant révocation: %v", err)
	}

	if err := svc.Revoke(ctx, sessionID, 1); err != nil {
		t.Fatalf("révocation: %v", err)
	}
	if _, _, err := svc.PublicView(ctx, share.Token); err != service.ErrNotFound {
		t.Fatalf("après révocation la vue publique doit renvoyer ErrNotFound, obtenu: %v", err)
	}
}

func TestShareRevokeRefusesNonOwner(t *testing.T) {
	svc, sessionID := newShareService(t, 1, nil)
	ctx := context.Background()
	if _, err := svc.CreateOrGet(ctx, sessionID, 1); err != nil {
		t.Fatalf("partage: %v", err)
	}
	if err := svc.Revoke(ctx, sessionID, 2); err != service.ErrForbidden {
		t.Fatalf("un non-propriétaire ne doit pas pouvoir révoquer, obtenu: %v", err)
	}
}

func TestPublicViewUnknownToken(t *testing.T) {
	svc, _ := newShareService(t, 1, nil)
	ctx := context.Background()
	if _, _, err := svc.PublicView(ctx, "jeton-inexistant"); err != service.ErrNotFound {
		t.Fatalf("un jeton inconnu doit renvoyer ErrNotFound, obtenu: %v", err)
	}
}

func TestPublicViewSnapshotFiltering(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	messages := []*model.Message{
		{Role: model.MessageRoleUser, Content: "question 1", CreatedAt: base},
		{Role: model.MessageRoleAssistant, Content: "réponse 1", CreatedAt: base.Add(time.Minute)},
		// Résumé interne : ne doit jamais fuiter.
		{Role: model.MessageRoleSummary, Content: "résumé interne", CreatedAt: base.Add(2 * time.Minute)},
		// Tour d'outil : rôle assistant, mais mécanique interne — jamais exposé.
		{Role: model.MessageRoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-1", Name: "search", Arguments: "{}"}}, CreatedAt: base.Add(3 * time.Minute)},
		{Role: model.MessageRoleTool, ToolCallID: "call-1", ToolName: "search", Content: "extrait interne", CreatedAt: base.Add(4 * time.Minute)},
		// Message postérieur au partage : exclu de l'instantané figé.
		{Role: model.MessageRoleUser, Content: "question ultérieure", CreatedAt: base.Add(time.Hour)},
	}
	svc, sessionID := newShareService(t, 1, messages)
	ctx := context.Background()

	share, err := svc.CreateOrGet(ctx, sessionID, 1)
	if err != nil {
		t.Fatalf("partage: %v", err)
	}
	// SharedAt vaut time.Now() (postérieur à tous les messages de base) : la
	// borne temporelle les inclut donc tous. Ce test vérifie l'exclusion des
	// rôles internes ; l'exclusion temporelle est couverte par
	// TestPublicViewExcludesLaterMessages.
	_, visible, err := svc.PublicView(ctx, share.Token)
	if err != nil {
		t.Fatalf("vue publique: %v", err)
	}
	for _, m := range visible {
		if m.Role == model.MessageRoleSummary || m.Role == model.MessageRoleSystem {
			t.Fatalf("un rôle interne (%s) ne doit jamais être exposé publiquement", m.Role)
		}
		if m.IsToolTurn() {
			t.Fatalf("un tour d'outil ne doit jamais être exposé publiquement: %+v", m)
		}
	}
	if len(visible) != 3 {
		t.Fatalf("attendu les 3 messages conversationnels, obtenu %d", len(visible))
	}
}

func TestPublicViewExcludesLaterMessages(t *testing.T) {
	// Sème une session dont le partage est créé, puis ajoute un message
	// postérieur : il ne doit pas apparaître dans l'instantané figé.
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gormrepo.Open(dbPath)
	if err != nil {
		t.Fatalf("ouverture base: %v", err)
	}
	ctx := context.Background()
	sessions := gormrepo.NewSessionRepository(db)
	msgs := gormrepo.NewMessageRepository(db)
	shares := gormrepo.NewShareRepository(db)
	svc := service.NewShareService(shares, sessions, msgs)

	sess := &model.Session{ProjectID: "p", UserID: 1, Status: model.SessionStatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := sessions.Save(ctx, sess); err != nil {
		t.Fatalf("session: %v", err)
	}
	if err := msgs.Save(ctx, &model.Message{SessionID: sess.ID, Role: model.MessageRoleUser, Content: "avant", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("message avant: %v", err)
	}

	share, err := svc.CreateOrGet(ctx, sess.ID, 1)
	if err != nil {
		t.Fatalf("partage: %v", err)
	}

	// Message ajouté après le partage.
	if err := msgs.Save(ctx, &model.Message{SessionID: sess.ID, Role: model.MessageRoleUser, Content: "après", CreatedAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("message après: %v", err)
	}

	_, visible, err := svc.PublicView(ctx, share.Token)
	if err != nil {
		t.Fatalf("vue publique: %v", err)
	}
	for _, m := range visible {
		if m.Content == "après" {
			t.Fatal("un message postérieur au partage ne doit pas apparaître dans l'instantané")
		}
	}
	if len(visible) != 1 {
		t.Fatalf("attendu 1 message figé, obtenu %d", len(visible))
	}
}
