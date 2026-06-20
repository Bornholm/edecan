package llm

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"

	"github.com/bornholm/genai/llm"
	"github.com/bornholm/genai/mcp/common"
	goMCP "github.com/modelcontextprotocol/go-sdk/mcp"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

// Établissement de la connexion à un serveur MCP au (re)démarrage d'une
// session : bornes des tentatives et backoff exponentiel initial, pour
// absorber une indisponibilité transitoire du serveur (redémarrage, latence
// réseau) avant d'abandonner. La reconnexion *en cours* d'appel d'outil est,
// elle, gérée par common.Client (callToolWithReconnect).
const (
	mcpConnectMaxAttempts = 3
	mcpConnectBaseDelay   = 500 * time.Millisecond
)

// sessionTools regroupe les outils résolus pour une session de chat ainsi
// que les clients MCP sous-jacents — conservés pour pouvoir les arrêter
// proprement (cf. ChatAgent.ForgetSession), libérant la ressource gérée par
// le serveur MCP pour cette session (ex. un workspace de bac à sable).
type sessionTools struct {
	tools   []llm.Tool
	clients []*common.Client
}

func (st *sessionTools) stop() {
	for _, c := range st.clients {
		_ = c.Stop()
	}
}

// newSessionTools se connecte à chaque serveur MCP déclaré par l'agent et
// agrège les outils qu'ils exposent. Chaque connexion est retentée en cas
// d'échec transitoire (cf. connectServer) ; si un serveur reste inaccessible
// après épuisement des tentatives, la session ne peut pas démarrer et l'erreur
// est remontée — les serveurs déjà connectés sont alors libérés pour ne pas
// fuir de ressource.
//
// Transport Streamable HTTP (https://modelcontextprotocol.io/specification
// /2025-11-25/basic/transports#streamable-http), pas SSE : c'est celui
// qu'exposent les serveurs MCP du SDK officiel construits via
// mcp.NewStreamableHTTPHandler (ex. LeaSH).
func newSessionTools(ctx context.Context, servers []model.MCPServer) (*sessionTools, error) {
	st := &sessionTools{}
	for _, s := range servers {
		client, tools, err := connectServer(ctx, s)
		if err != nil {
			st.stop()
			return nil, err
		}
		st.clients = append(st.clients, client)
		st.tools = append(st.tools, tools...)
	}
	return st, nil
}

// connectServer établit la connexion à s et récupère ses outils, avec jusqu'à
// mcpConnectMaxAttempts tentatives espacées d'un backoff exponentiel. Une
// erreur de configuration (transport inconnu, en-tête invalide) n'est pas
// transitoire et échoue immédiatement, sans réessai. Après épuisement des
// tentatives sur une indisponibilité, la dernière erreur est remontée
// enrichie du nombre d'essais.
func connectServer(ctx context.Context, s model.MCPServer) (*common.Client, []llm.Tool, error) {
	client, err := newClientForServer(s)
	if err != nil {
		return nil, nil, err
	}

	backoff := mcpConnectBaseDelay
	var lastErr error
	for attempt := 1; attempt <= mcpConnectMaxAttempts; attempt++ {
		tools, err := startAndListTools(ctx, client, s)
		if err == nil {
			return client, tools, nil
		}
		lastErr = err
		// Repart d'une session propre avant de retenter : Start rétablit la
		// connexion via le connector.
		_ = client.Stop()

		if attempt < mcpConnectMaxAttempts {
			slog.WarnContext(ctx, "connexion au serveur MCP échouée, nouvelle tentative",
				"server", s.Name, "attempt", attempt, "max", mcpConnectMaxAttempts, "backoff", backoff, "error", err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
			backoff *= 2
		}
	}
	return nil, nil, fmt.Errorf("connexion au serveur MCP %q après %d tentatives: %w", s.Name, mcpConnectMaxAttempts, lastErr)
}

// startAndListTools ouvre la connexion et liste les outils exposés — les deux
// étapes sont retentées ensemble par connectServer, une connexion à demi
// établie (Start réussi, GetTools en échec) devant elle aussi être rejouée.
func startAndListTools(ctx context.Context, client *common.Client, s model.MCPServer) ([]llm.Tool, error) {
	if err := client.Start(ctx); err != nil {
		return nil, fmt.Errorf("connexion au serveur MCP %q: %w", s.Name, err)
	}
	tools, err := client.GetTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("récupération des outils du serveur MCP %q: %w", s.Name, err)
	}
	return tools, nil
}

// newClientForServer construit le client MCP adapté au transport déclaré par
// le serveur (Streamable HTTP distant ou stdio local).
func newClientForServer(s model.MCPServer) (*common.Client, error) {
	switch s.Transport {
	case model.MCPTransportStdio:
		return newStdioClient(s.Command, s.Args, s.Env), nil
	case model.MCPTransportHTTP, "":
		httpClient, err := httpClientWithHeaders(s.Headers)
		if err != nil {
			return nil, fmt.Errorf("en-têtes du serveur MCP %q: %w", s.Name, err)
		}
		return newStreamableClient(s.URL, httpClient), nil
	default:
		return nil, fmt.Errorf("serveur MCP %q: transport %q inconnu", s.Name, s.Transport)
	}
}

// newStreamableClient construit un client MCP générique (common.Client,
// gestion des outils et reconnexion déjà fournies par genai/mcp/common) au
// dessus du transport Streamable HTTP du SDK officiel.
func newStreamableClient(endpoint string, httpClient *http.Client) *common.Client {
	connector := common.ConnectorFunc(func(ctx context.Context) (*goMCP.ClientSession, error) {
		client := goMCP.NewClient(&goMCP.Implementation{Name: "edecan", Version: "v1.0.0"}, nil)
		transport := &goMCP.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient}
		return client.Connect(ctx, transport, nil)
	})
	return common.NewClient(connector)
}

// newStdioClient construit un client MCP générique au dessus du transport
// stdio du SDK officiel : le serveur MCP est lancé comme sous-processus
// (command + args) et le dialogue passe par ses stdin/stdout. env ajoute des
// variables à l'environnement du serveur edecán. Un nouveau *exec.Cmd est
// construit à chaque connexion (le connector peut être rappelé lors d'une
// reconnexion), un *exec.Cmd n'étant pas réutilisable.
func newStdioClient(command string, args []string, env map[string]string) *common.Client {
	connector := common.ConnectorFunc(func(ctx context.Context) (*goMCP.ClientSession, error) {
		client := goMCP.NewClient(&goMCP.Implementation{Name: "edecan", Version: "v1.0.0"}, nil)
		cmd := exec.CommandContext(ctx, command, args...)
		if len(env) > 0 {
			cmd.Env = os.Environ()
			for k, v := range env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
		}
		transport := &goMCP.CommandTransport{Command: cmd}
		return client.Connect(ctx, transport, nil)
	})
	return common.NewClient(connector)
}

// httpClientWithHeaders construit un *http.Client qui injecte headers sur
// chaque requête — utilisé pour l'authentification au serveur MCP (jeton,
// clé d'API...) et pour scoper ses ressources par session/utilisateur (ex.
// "X-Workspace"), portée par model.MCPServer.Headers. Chaque valeur est un
// template Go (text/template) exécuté à chaque requête contre
// port.MCPIdentityFromContext — voir headerTransport.
func httpClientWithHeaders(headers map[string]string) (*http.Client, error) {
	if len(headers) == 0 {
		return http.DefaultClient, nil
	}
	compiled := make(map[string]*template.Template, len(headers))
	for k, v := range headers {
		tmpl, err := template.New(k).Parse(v)
		if err != nil {
			return nil, fmt.Errorf("en-tête %q: modèle invalide: %w", k, err)
		}
		compiled[k] = tmpl
	}
	return &http.Client{Transport: &headerTransport{headers: compiled, base: http.DefaultTransport}}, nil
}

type headerTransport struct {
	headers map[string]*template.Template
	base    http.RoundTripper
}

// RoundTrip implements http.RoundTripper. Clone la requête avant de la
// modifier : un RoundTripper ne doit jamais muter la requête de l'appelant.
// Chaque template est exécuté ici, à chaque requête (et non une seule fois
// à la construction du client) — c'est ce qui garantit que l'appel
// d'établissement de session MCP ("initialize") porte la bonne valeur,
// puisque edecán établit une connexion MCP distincte par session de chat
// (cf. ChatAgent.toolsForSession).
func (t *headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	identity, _ := port.MCPIdentityFromContext(r.Context())

	cloned := r.Clone(r.Context())
	for k, tmpl := range t.headers {
		var buf strings.Builder
		if err := tmpl.Execute(&buf, identity); err != nil {
			return nil, fmt.Errorf("rendu de l'en-tête MCP %q: %w", k, err)
		}
		cloned.Header.Set(k, buf.String())
	}
	return t.base.RoundTrip(cloned)
}
