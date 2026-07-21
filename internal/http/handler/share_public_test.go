package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"edecan/internal/core/model"
	"edecan/internal/core/service"
	gormrepo "edecan/internal/infra/persistence/gorm"
)

// newPublicTestHandlers monte des Handlers minimaux (seul ShareService est
// requis pour la route publique) adossés à une base temporaire, sème une
// session partagée et retourne le jeton du partage actif.
func newPublicTestHandlers(t *testing.T) (*Handlers, model.ShareToken, model.SessionID) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gormrepo.Open(dbPath)
	if err != nil {
		t.Fatalf("ouverture base: %v", err)
	}
	ctx := context.Background()
	sessions := gormrepo.NewSessionRepository(db)
	msgs := gormrepo.NewMessageRepository(db)
	shares := gormrepo.NewShareRepository(db)
	shareService := service.NewShareService(shares, sessions, msgs)

	sess := &model.Session{ProjectID: "p", UserID: 1, Title: "Session confidentielle", Status: model.SessionStatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := sessions.Save(ctx, sess); err != nil {
		t.Fatalf("session: %v", err)
	}
	if err := msgs.Save(ctx, &model.Message{SessionID: sess.ID, Role: model.MessageRoleUser, Content: "Bonjour, j'ai un souci", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("message: %v", err)
	}

	share, err := shareService.CreateOrGet(ctx, sess.ID, 1)
	if err != nil {
		t.Fatalf("partage: %v", err)
	}

	h := &Handlers{ShareService: shareService, BaseURL: "https://exemple.test"}
	return h, share.Token, sess.ID
}

func TestPublicShareHandlerServesConversation(t *testing.T) {
	h, token, _ := newPublicTestHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/share/"+string(token), nil)
	req.SetPathValue("token", string(token))
	rec := httptest.NewRecorder()

	h.PublicShareHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", rec.Code)
	}
	if got := rec.Header().Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Fatalf("l'en-tête X-Robots-Tag doit refuser l'indexation, obtenu: %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Bonjour, j&#39;ai un souci") && !strings.Contains(body, "Bonjour, j'ai un souci") {
		t.Fatal("le contenu du message devrait être présent dans la page publique")
	}
	// Absence de fuite : ni rail, ni composer, ni panneau projets/sessions.
	for _, leak := range []string{"edc-rail", "edc-chat__composer", "modal-root"} {
		if strings.Contains(body, leak) {
			t.Fatalf("la page publique ne doit pas contenir %q (fuite de chrome privé)", leak)
		}
	}
}

func TestPublicShareHandlerRevokedReturns404(t *testing.T) {
	h, token, sessionID := newPublicTestHandlers(t)

	if err := h.ShareService.Revoke(context.Background(), sessionID, 1); err != nil {
		t.Fatalf("révocation: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/share/"+string(token), nil)
	req.SetPathValue("token", string(token))
	rec := httptest.NewRecorder()

	h.PublicShareHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("un partage révoqué doit renvoyer 404, obtenu %d", rec.Code)
	}
}

func TestPublicShareHandlerUnknownReturns404(t *testing.T) {
	h, _, _ := newPublicTestHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/share/inconnu", nil)
	req.SetPathValue("token", "inconnu")
	rec := httptest.NewRecorder()

	h.PublicShareHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("un jeton inconnu doit renvoyer 404, obtenu %d", rec.Code)
	}
}
