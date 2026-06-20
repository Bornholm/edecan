package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	goMCP "github.com/modelcontextprotocol/go-sdk/mcp"

	"edecan/internal/core/model"
)

// newTestMCPServer démarre un serveur MCP Streamable HTTP en mémoire exposant
// un unique outil "ping", éventuellement précédé d'un middleware qui fait
// échouer les premières requêtes (failFirst) pour simuler une indisponibilité
// transitoire du serveur.
func newTestMCPServer(t *testing.T, failFirst int32) *httptest.Server {
	t.Helper()
	server := goMCP.NewServer(&goMCP.Implementation{Name: "test", Version: "v1"}, nil)
	goMCP.AddTool(server, &goMCP.Tool{Name: "ping", Description: "ping"},
		func(ctx context.Context, req *goMCP.CallToolRequest, _ any) (*goMCP.CallToolResult, any, error) {
			return &goMCP.CallToolResult{Content: []goMCP.Content{&goMCP.TextContent{Text: "pong"}}}, nil, nil
		})

	handler := goMCP.NewStreamableHTTPHandler(func(*http.Request) *goMCP.Server { return server }, nil)

	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&count, 1) <= failFirst {
			http.Error(w, "indisponible", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestConnectServerHappyPath : un serveur sain se connecte du premier coup et
// ses outils sont exposés.
func TestConnectServerHappyPath(t *testing.T) {
	srv := newTestMCPServer(t, 0)
	client, tools, err := connectServer(context.Background(), model.MCPServer{
		Name: "test", Transport: model.MCPTransportHTTP, URL: srv.URL,
	})
	if err != nil {
		t.Fatalf("connexion inattendue en échec: %v", err)
	}
	defer client.Stop()
	if len(tools) != 1 || tools[0].Name() != "ping" {
		t.Fatalf("outils attendus [ping], obtenus %v", tools)
	}
}

// TestConnectServerRetriesThenSucceeds : la première tentative échoue
// (503), la connexion est renouvelée et réussit à la seconde.
func TestConnectServerRetriesThenSucceeds(t *testing.T) {
	// failFirst=1 : seule la toute première requête (l'initialize de la 1re
	// tentative) échoue ; la 2e tentative repart sur un initialize neuf.
	srv := newTestMCPServer(t, 1)
	client, tools, err := connectServer(context.Background(), model.MCPServer{
		Name: "test", Transport: model.MCPTransportHTTP, URL: srv.URL,
	})
	if err != nil {
		t.Fatalf("la reconnexion aurait dû réussir: %v", err)
	}
	defer client.Stop()
	if len(tools) != 1 {
		t.Fatalf("un outil attendu, obtenus %d", len(tools))
	}
}

// TestConnectServerFailsAfterRetries : un serveur toujours indisponible fait
// échouer connectServer après épuisement des tentatives — la session se coupe
// en erreur (SPEC : gestion des erreurs MCP).
func TestConnectServerFailsAfterRetries(t *testing.T) {
	srv := newTestMCPServer(t, 1<<30) // échoue toujours
	_, _, err := connectServer(context.Background(), model.MCPServer{
		Name: "test", Transport: model.MCPTransportHTTP, URL: srv.URL,
	})
	if err == nil {
		t.Fatal("une erreur était attendue après épuisement des tentatives")
	}
}

// TestNewSessionToolsStopsOnPartialFailure : si un second serveur est
// injoignable, la session ne démarre pas et le premier serveur (sain) est
// bien libéré — pas de connexion orpheline.
func TestNewSessionToolsStopsOnPartialFailure(t *testing.T) {
	good := newTestMCPServer(t, 0)
	bad := newTestMCPServer(t, 1<<30)

	_, err := newSessionTools(context.Background(), []model.MCPServer{
		{Name: "good", Transport: model.MCPTransportHTTP, URL: good.URL},
		{Name: "bad", Transport: model.MCPTransportHTTP, URL: bad.URL},
	})
	if err == nil {
		t.Fatal("newSessionTools aurait dû échouer sur le second serveur")
	}
}
