package llm

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/bornholm/genai/llm"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

// ChatAgent implémente port.ChatAgent au-dessus d'un llm.Client genai.
//
// Les serveurs MCP de l'agent ne sont pas résolus une fois pour toutes :
// chaque session de chat obtient sa propre connexion MCP (cf.
// toolsForSession), créée à la demande lors de son premier message
// nécessitant des outils — nécessaire pour que le templating des en-têtes
// (cf. mcp.go, port.MCPIdentity) puisse réellement scoper les ressources
// d'un serveur MCP par session : certains serveurs MCP (ex. LeaSH) résolvent
// ce scope une seule fois, à l'établissement de la connexion, jamais à
// chaque appel d'outil.
type ChatAgent struct {
	client     llm.Client
	mcpServers []model.MCPServer

	mu             sync.Mutex
	toolsBySession map[string]*sessionTools
}

// NewChatAgent construit un port.ChatAgent à partir d'un llm.Client déjà
// configuré pour le provider de l'agent (voir NewClient), et des serveurs
// MCP de l'agent — vide si l'agent n'en déclare aucun.
func NewChatAgent(client llm.Client, mcpServers []model.MCPServer) *ChatAgent {
	return &ChatAgent{client: client, mcpServers: mcpServers}
}

var _ port.ChatAgent = (*ChatAgent)(nil)

// StreamReply envoie l'historique à l'agent et retransmet les fragments.
// Le consommateur DOIT lire jusqu'à fermeture du channel ou annulation de
// ctx pour éviter toute fuite de goroutine (cf. PLAN.md §Phase 4).
//
// Sans serveur MCP configuré, la réponse est streamée token par token
// (comportement historique). Avec des outils disponibles, la résolution des
// appels d'outils passe par de simples appels de complétion (non streamés —
// reconstruire des appels d'outils à partir de deltas streamés est
// spécifique à chaque provider) ; seule la réponse finale, une fois tous les
// outils résolus, est livrée au consommateur (cf. streamWithTools).
func (a *ChatAgent) StreamReply(ctx context.Context, agent model.Agent, history []model.Message) (<-chan port.ChatChunk, error) {
	messages := toLLMMessages(agent.SystemPrompt, history)

	if len(a.mcpServers) == 0 {
		return a.streamPlain(ctx, messages, agent.MaxCompletionTokens)
	}

	tools, err := a.toolsForSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("connexion aux serveurs MCP: %w", err)
	}
	return a.streamWithTools(ctx, messages, tools, agent.MaxCompletionTokens, agent.MaxSequentialToolCalls)
}

// toolsForSession retourne les outils de la session portée par ctx (cf.
// port.MCPIdentityFromContext), en réutilisant la connexion déjà établie
// pour cette session le cas échéant — une seule connexion MCP par session
// de chat, réutilisée pour tous ses messages (cf. doc ChatAgent).
func (a *ChatAgent) toolsForSession(ctx context.Context) ([]llm.Tool, error) {
	identity, _ := port.MCPIdentityFromContext(ctx)
	key := identity.SessionID

	a.mu.Lock()
	if st, ok := a.toolsBySession[key]; ok {
		a.mu.Unlock()
		slog.DebugContext(ctx, "réutilisation de la connexion MCP de la session", "session", key, "tools", len(st.tools))
		return st.tools, nil
	}
	a.mu.Unlock()

	// L'établissement de la connexion (I/O réseau + backoff de reconnexion) se
	// fait hors du verrou : une session dont le serveur MCP est lent ou en
	// échec ne doit pas bloquer les autres sessions le temps de ses tentatives.
	st, err := newSessionTools(ctx, a.mcpServers)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	// Course bénigne : une autre requête de la même session a pu établir la
	// connexion pendant notre tentative — on conserve la sienne (déjà en
	// cache) et on libère la nôtre pour ne pas laisser une connexion orpheline.
	if existing, ok := a.toolsBySession[key]; ok {
		st.stop()
		return existing.tools, nil
	}
	slog.InfoContext(ctx, "connexion MCP établie pour la session", "session", key, "tools", len(st.tools))
	if a.toolsBySession == nil {
		a.toolsBySession = make(map[string]*sessionTools)
	}
	a.toolsBySession[key] = st
	return st.tools, nil
}

// ForgetSession implémente port.ChatAgent.
func (a *ChatAgent) ForgetSession(sessionID string) {
	a.mu.Lock()
	st, ok := a.toolsBySession[sessionID]
	delete(a.toolsBySession, sessionID)
	a.mu.Unlock()

	if ok {
		st.stop()
	}
}

func (a *ChatAgent) streamPlain(ctx context.Context, messages []llm.Message, maxCompletionTokens int) (<-chan port.ChatChunk, error) {
	stream, err := a.client.ChatCompletionStream(ctx, llm.WithMessages(messages...), llm.WithMaxCompletionTokens(maxCompletionTokens))
	if err != nil {
		return nil, fmt.Errorf("démarrage du streaming LLM: %w", err)
	}

	out := make(chan port.ChatChunk)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-stream:
				if !ok {
					return
				}
				if err := chunk.Error(); err != nil {
					select {
					case out <- port.ChatChunk{Err: err}:
					case <-ctx.Done():
					}
					return
				}
				result := port.ChatChunk{Done: chunk.IsComplete()}
				if delta := chunk.Delta(); delta != nil {
					result.Content = delta.Content()
				}
				select {
				case out <- result:
				case <-ctx.Done():
					return
				}
				if result.Done {
					return
				}
			}
		}
	}()

	return out, nil
}

// streamWithTools résout les appels d'outils par itérations successives de
// complétion non streamée (cf. StreamReply), puis livre la réponse finale en
// un seul fragment de contenu suivi du fragment Done.
//
// maxIterations borne le nombre d'allers-retours outil↔LLM enchaînés. Une
// fois ce plafond atteint sans que le modèle n'ait produit de réponse
// textuelle, une dernière complétion est demandée en interdisant tout nouvel
// appel d'outil (ToolChoiceNone) : l'agent doit répondre à l'utilisateur à
// partir du contexte déjà collecté, plutôt que de renvoyer une erreur ou de
// continuer à appeler des outils indéfiniment.
func (a *ChatAgent) streamWithTools(ctx context.Context, messages []llm.Message, tools []llm.Tool, maxCompletionTokens, maxIterations int) (<-chan port.ChatChunk, error) {
	out := make(chan port.ChatChunk)
	go func() {
		defer close(out)

		for i := 0; i < maxIterations; i++ {
			resp, err := a.client.ChatCompletion(ctx,
				llm.WithMessages(messages...),
				llm.WithTools(tools...),
				llm.WithToolChoice(llm.ToolChoiceAuto),
				llm.WithMaxCompletionTokens(maxCompletionTokens),
			)
			if err != nil {
				sendOrCancel(ctx, out, port.ChatChunk{Err: fmt.Errorf("complétion LLM avec outils: %w", err)})
				return
			}

			toolCalls := resp.ToolCalls()
			slog.DebugContext(ctx, "réponse LLM avec outils", "iteration", i, "tools_proposed", len(tools), "tool_calls", len(toolCalls))
			if len(toolCalls) == 0 {
				if !sendOrCancel(ctx, out, port.ChatChunk{Content: resp.Message().Content()}) {
					return
				}
				sendOrCancel(ctx, out, port.ChatChunk{Done: true})
				return
			}

			messages = append(messages, llm.NewToolCallsMessage(toolCalls...))
			for _, tc := range toolCalls {
				slog.InfoContext(ctx, "appel d'outil MCP", "name", tc.Name())
				// Retour visuel dans le chat : signale l'appel d'outil avant de
				// l'exécuter, pour que l'utilisateur voie l'agent « travailler »
				// pendant la résolution (souvent plus lente qu'un token).
				if !sendOrCancel(ctx, out, port.ChatChunk{Tool: &port.ToolActivity{Name: tc.Name()}}) {
					return
				}
				toolMsg, err := llm.ExecuteToolCall(ctx, tc, tools...)
				if err != nil {
					sendOrCancel(ctx, out, port.ChatChunk{Err: fmt.Errorf("exécution de l'outil %q: %w", tc.Name(), err)})
					return
				}
				messages = append(messages, toolMsg)
			}
		}

		// Plafond d'appels d'outils atteint : on force une réponse finale sans
		// nouvel appel d'outil, à partir de tout ce qui a été collecté.
		slog.WarnContext(ctx, "plafond d'appels d'outils atteint, réponse finale forcée", "max_iterations", maxIterations)
		resp, err := a.client.ChatCompletion(ctx,
			llm.WithMessages(messages...),
			llm.WithTools(tools...),
			llm.WithToolChoice(llm.ToolChoiceNone),
			llm.WithMaxCompletionTokens(maxCompletionTokens),
		)
		if err != nil {
			sendOrCancel(ctx, out, port.ChatChunk{Err: fmt.Errorf("réponse finale après plafond d'appels d'outils: %w", err)})
			return
		}
		if !sendOrCancel(ctx, out, port.ChatChunk{Content: resp.Message().Content()}) {
			return
		}
		sendOrCancel(ctx, out, port.ChatChunk{Done: true})
	}()

	return out, nil
}

// sendOrCancel envoie chunk sur out, sauf si ctx est annulé entre-temps.
// Retourne false si l'envoi n'a pas eu lieu (ctx annulé) — signal pour
// l'appelant d'arrêter immédiatement, sans tenter d'envoi supplémentaire.
func sendOrCancel(ctx context.Context, out chan<- port.ChatChunk, chunk port.ChatChunk) bool {
	select {
	case out <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

// Summarize condense les messages antérieurs (SPEC §Chat, point 11).
func (a *ChatAgent) Summarize(ctx context.Context, agent model.Agent, history []model.Message) (string, error) {
	const summaryInstruction = "Résume la conversation précédente de façon concise, en conservant les faits et décisions importants. Réponds uniquement avec le résumé en Markdown."

	messages := toLLMMessages(agent.SystemPrompt, history)
	messages = append(messages, llm.NewMessage(llm.RoleUser, summaryInstruction))

	resp, err := a.client.ChatCompletion(ctx, llm.WithMessages(messages...))
	if err != nil {
		return "", fmt.Errorf("génération du résumé: %w", err)
	}
	return resp.Message().Content(), nil
}

// DraftTicket génère un brouillon de ticket (titre + corps Markdown) à
// partir des échanges (SPEC §Handover, point 14).
func (a *ChatAgent) DraftTicket(ctx context.Context, agent model.Agent, history []model.Message) (title string, body string, err error) {
	const draftInstruction = "À partir de la conversation précédente, rédige un brouillon de ticket de support. " +
		"Réponds STRICTEMENT au format suivant, sans texte additionnel :\n" +
		"TITRE: <titre court>\n" +
		"---\n" +
		"<corps du ticket en Markdown>"

	messages := toLLMMessages(agent.SystemPrompt, history)
	messages = append(messages, llm.NewMessage(llm.RoleUser, draftInstruction))

	resp, err := a.client.ChatCompletion(ctx, llm.WithMessages(messages...))
	if err != nil {
		return "", "", fmt.Errorf("génération du brouillon de ticket: %w", err)
	}

	return parseDraft(resp.Message().Content())
}

func toLLMMessages(systemPrompt string, history []model.Message) []llm.Message {
	messages := make([]llm.Message, 0, len(history)+1)
	if systemPrompt != "" {
		messages = append(messages, llm.NewMessage(llm.RoleSystem, systemPrompt))
	}
	for _, m := range history {
		messages = append(messages, llm.NewMessage(toLLMRole(m.Role), m.Content))
	}
	return messages
}

func toLLMRole(role model.MessageRole) llm.Role {
	switch role {
	case model.MessageRoleAssistant:
		return llm.RoleAssistant
	case model.MessageRoleSystem, model.MessageRoleSummary:
		return llm.RoleSystem
	default:
		return llm.RoleUser
	}
}

// parseDraft extrait titre et corps de la réponse formatée du LLM. En cas de
// format inattendu, le corps complet est conservé et le titre est laissé
// vide — l'utilisateur édite de toute façon le brouillon avant soumission
// (SPEC §Edge Cases : échec de génération du brouillon → brouillon vide éditable).
func parseDraft(content string) (title string, body string, err error) {
	const prefix = "TITRE:"
	const sep = "\n---\n"

	if !strings.HasPrefix(strings.TrimSpace(content), prefix) {
		return "", strings.TrimSpace(content), nil
	}

	parts := strings.SplitN(content, sep, 2)
	firstLine, rest := parts[0], ""
	if len(parts) == 2 {
		rest = parts[1]
	}

	title = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(firstLine), prefix))
	body = strings.TrimSpace(rest)
	return title, body, nil
}
