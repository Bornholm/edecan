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

// newChatService monte un ChatService adossé à une base SQLite temporaire et y
// sème une session appartenant à ownerID. Aucun projet ni agent n'est déclaré :
// les cas testés ici ne sollicitent pas le LLM.
func newChatService(t *testing.T, ownerID model.UserID) (*service.ChatService, model.SessionID) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gormrepo.Open(dbPath)
	if err != nil {
		t.Fatalf("ouverture base de test: %v", err)
	}
	sessions := gormrepo.NewSessionRepository(db)
	msgs := gormrepo.NewMessageRepository(db)
	flags := gormrepo.NewRelevanceFlagRepository(db)

	sess := &model.Session{
		ProjectID: "projet-test",
		UserID:    ownerID,
		Title:     "Ma session",
		Status:    model.SessionStatusActive,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := sessions.Save(context.Background(), sess); err != nil {
		t.Fatalf("création session: %v", err)
	}

	svc := service.NewChatService(sessions, msgs, flags, nil, nil, nil, nil, nil)
	return svc, sess.ID
}

// TestFinalizeReplyPersistsToolTurns : les allers-retours d'outils résolus
// pendant la génération doivent être enregistrés avant la réponse et dans leur
// ordre d'apparition — c'est cet historique qui permet à l'agent de retrouver,
// au tour suivant, ce que sa recherche a retourné.
func TestFinalizeReplyPersistsToolTurns(t *testing.T) {
	svc, sessionID := newChatService(t, 1)
	ctx := context.Background()

	toolTurns := []model.Message{
		{Role: model.MessageRoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "search", Arguments: `{"q":"code postal"}`},
		}},
		{Role: model.MessageRoleTool, ToolCallID: "call-1", ToolName: "search", Content: "extrait de ProfileType.php"},
	}

	if err := svc.FinalizeReply(ctx, sessionID, "le champ est conditionnel", "", toolTurns); err != nil {
		t.Fatalf("finalisation: %v", err)
	}

	messages, err := svc.ListMessages(ctx, sessionID, 1)
	if err != nil {
		t.Fatalf("relecture: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("attendu 3 messages persistés (appels, résultat, réponse), obtenu %d", len(messages))
	}

	calls := messages[0]
	if len(calls.ToolCalls) != 1 {
		t.Fatalf("appels d'outils non restitués: %+v", calls)
	}
	if calls.ToolCalls[0].ID != "call-1" || calls.ToolCalls[0].Arguments != `{"q":"code postal"}` {
		t.Fatalf("appel mal restitué: %+v", calls.ToolCalls[0])
	}
	if calls.SessionID != sessionID {
		t.Fatalf("le tour d'outil doit être rattaché à la session %d, obtenu %d", sessionID, calls.SessionID)
	}

	result := messages[1]
	if result.Role != model.MessageRoleTool || result.ToolCallID != "call-1" {
		t.Fatalf("résultat d'outil mal restitué: %+v", result)
	}
	if result.Content != "extrait de ProfileType.php" {
		t.Fatalf("contenu du résultat attendu, obtenu %q", result.Content)
	}

	reply := messages[2]
	if reply.Role != model.MessageRoleAssistant || reply.Content != "le champ est conditionnel" {
		t.Fatalf("réponse finale attendue en dernier, obtenu %+v", reply)
	}
	if reply.IsToolTurn() {
		t.Fatal("la réponse finale ne doit pas être vue comme un tour d'outil")
	}
}

// TestFinalizeReplyWithoutToolTurns : sans outil mobilisé, seule la réponse est
// persistée (comportement historique inchangé).
func TestFinalizeReplyWithoutToolTurns(t *testing.T) {
	svc, sessionID := newChatService(t, 1)
	ctx := context.Background()

	if err := svc.FinalizeReply(ctx, sessionID, "bonjour", "un raisonnement", nil); err != nil {
		t.Fatalf("finalisation: %v", err)
	}

	messages, err := svc.ListMessages(ctx, sessionID, 1)
	if err != nil {
		t.Fatalf("relecture: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("attendu 1 message, obtenu %d", len(messages))
	}
	if messages[0].Reasoning != "un raisonnement" {
		t.Fatalf("raisonnement attendu, obtenu %q", messages[0].Reasoning)
	}
	if len(messages[0].ToolCalls) != 0 {
		t.Fatalf("aucun appel d'outil attendu, obtenu %d", len(messages[0].ToolCalls))
	}
}
