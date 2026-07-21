package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/bornholm/genai/llm"

	"edecan/internal/core/port"
)

// stubResponse implémente llm.ChatCompletionResponse.
type stubResponse struct {
	content   string
	toolCalls []llm.ToolCall
}

func (r stubResponse) Message() llm.Message           { return llm.NewMessage(llm.RoleAssistant, r.content) }
func (r stubResponse) ToolCalls() []llm.ToolCall      { return r.toolCalls }
func (r stubResponse) Usage() llm.ChatCompletionUsage { return nil }

// stubClient implémente llm.Client. Tant qu'il ne voit pas la consigne de
// conclusion injectée au plafond, il redemande un appel d'outil "search" ; dès
// que cette consigne apparaît en dernier message, il renvoie une réponse
// textuelle. Il compte les complétions et mémorise l'injection de la consigne.
type stubClient struct {
	completions    int
	autoCalls      int
	sawInstruction bool
}

func (c *stubClient) ChatCompletion(ctx context.Context, funcs ...llm.ChatCompletionOptionFunc) (llm.ChatCompletionResponse, error) {
	opts := &llm.ChatCompletionOptions{}
	for _, fn := range funcs {
		fn(opts)
	}
	c.completions++

	if len(opts.Messages) > 0 && opts.Messages[len(opts.Messages)-1].Content() == forceAnswerInstruction {
		c.sawInstruction = true
		return stubResponse{content: "réponse finale à partir du contexte"}, nil
	}
	c.autoCalls++
	return toolCallResponse(), nil
}

func (c *stubClient) ChatCompletionStream(ctx context.Context, funcs ...llm.ChatCompletionOptionFunc) (<-chan llm.StreamChunk, error) {
	panic("non utilisé")
}

func (c *stubClient) Embeddings(ctx context.Context, inputs []string, funcs ...llm.EmbeddingsOptionFunc) (llm.EmbeddingsResponse, error) {
	panic("non utilisé")
}

// testToolName et testToolCallID sont partagés par l'outil de test et les
// appels d'outil simulés : genai apparie un appel à son outil par le nom (cf.
// llm.ExecuteToolCall), donc une source unique évite tout couplage fragile
// entre le nom déclaré et le nom appelé.
const (
	testToolName   = "search"
	testToolCallID = "call-1"
)

// stubTool construit l'outil de test nommé testToolName : si execErr est non
// nil, son exécution échoue ; sinon elle renvoie result.
func stubTool(result string, execErr error) llm.Tool {
	return llm.NewFuncTool(testToolName, "outil de test", map[string]any{},
		func(ctx context.Context, params map[string]any) (llm.ToolResult, error) {
			if execErr != nil {
				return nil, execErr
			}
			return llm.NewToolResult(result), nil
		})
}

// toolCallResponse est une réponse LLM simulée déclenchant un appel de
// testToolName.
func toolCallResponse() stubResponse {
	return stubResponse{toolCalls: []llm.ToolCall{llm.NewToolCall(testToolCallID, testToolName, "{}")}}
}

func drainChunks(t *testing.T, ch <-chan port.ChatChunk) (content string, gotErr bool) {
	t.Helper()
	for chunk := range ch {
		if chunk.Err != nil {
			gotErr = true
		}
		content += chunk.Content
	}
	return content, gotErr
}

// TestStreamWithToolsForcesReplyAtCap : un agent qui rappelle sans cesse un
// outil doit, une fois le plafond atteint, produire une réponse finale (sans
// nouvel appel d'outil) plutôt qu'une erreur.
func TestStreamWithToolsForcesReplyAtCap(t *testing.T) {
	const maxIterations = 3
	client := &stubClient{}
	agent := NewChatAgent(client, nil)

	ch, err := agent.streamWithTools(context.Background(),
		[]llm.Message{llm.NewMessage(llm.RoleUser, "bonjour")},
		[]llm.Tool{stubTool("aucun résultat", nil)}, 256, maxIterations, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	content, gotErr := drainChunks(t, ch)

	if gotErr {
		t.Fatal("le plafond ne doit pas produire d'erreur, mais une réponse forcée")
	}
	if content != "réponse finale à partir du contexte" {
		t.Fatalf("réponse finale attendue, obtenu %q", content)
	}
	// maxIterations complétions en mode auto (toutes rendent un appel d'outil)
	// + 1 complétion finale après injection de la consigne de conclusion.
	if client.autoCalls != maxIterations {
		t.Fatalf("attendu %d appels auto, obtenu %d", maxIterations, client.autoCalls)
	}
	if client.completions != maxIterations+1 {
		t.Fatalf("attendu %d complétions au total, obtenu %d", maxIterations+1, client.completions)
	}
	if !client.sawInstruction {
		t.Fatal("la consigne de conclusion doit être injectée dans l'historique au plafond")
	}
}

// failOnceClient demande l'outil "search" à la première complétion, puis
// renvoie une réponse textuelle — pour vérifier le comportement après un échec
// d'outil.
type failOnceClient struct {
	completions int
}

func (c *failOnceClient) ChatCompletion(ctx context.Context, funcs ...llm.ChatCompletionOptionFunc) (llm.ChatCompletionResponse, error) {
	c.completions++
	if c.completions == 1 {
		return toolCallResponse(), nil
	}
	return stubResponse{content: "réponse malgré l'échec de l'outil"}, nil
}

func (c *failOnceClient) ChatCompletionStream(ctx context.Context, funcs ...llm.ChatCompletionOptionFunc) (<-chan llm.StreamChunk, error) {
	panic("non utilisé")
}

func (c *failOnceClient) Embeddings(ctx context.Context, inputs []string, funcs ...llm.EmbeddingsOptionFunc) (llm.EmbeddingsResponse, error) {
	panic("non utilisé")
}

// reasoningResponse implémente llm.ReasoningChatCompletionResponse : une
// réponse textuelle accompagnée d'un raisonnement.
type reasoningResponse struct {
	stubResponse
	reasoning string
}

func (r reasoningResponse) Reasoning() string                       { return r.reasoning }
func (r reasoningResponse) ReasoningDetails() []llm.ReasoningDetail { return nil }

// reasoningClient renvoie directement une réponse textuelle porteuse d'un
// raisonnement (aucun appel d'outil).
type reasoningClient struct{}

func (c *reasoningClient) ChatCompletion(ctx context.Context, funcs ...llm.ChatCompletionOptionFunc) (llm.ChatCompletionResponse, error) {
	return reasoningResponse{stubResponse: stubResponse{content: "la réponse"}, reasoning: "je réfléchis donc je suis"}, nil
}

func (c *reasoningClient) ChatCompletionStream(ctx context.Context, funcs ...llm.ChatCompletionOptionFunc) (<-chan llm.StreamChunk, error) {
	panic("non utilisé")
}

func (c *reasoningClient) Embeddings(ctx context.Context, inputs []string, funcs ...llm.EmbeddingsOptionFunc) (llm.EmbeddingsResponse, error) {
	panic("non utilisé")
}

// TestStreamWithToolsEmitsReasoning : lorsqu'un provider expose un raisonnement,
// il doit être remonté dans un ChatChunk.Reasoning, distinct du contenu.
func TestStreamWithToolsEmitsReasoning(t *testing.T) {
	agent := NewChatAgent(&reasoningClient{}, nil)

	ch, err := agent.streamWithTools(context.Background(),
		[]llm.Message{llm.NewMessage(llm.RoleUser, "coucou")},
		[]llm.Tool{stubTool("aucun résultat", nil)}, 256, 3, 0, "medium")
	if err != nil {
		t.Fatal(err)
	}

	var content, reasoning string
	for chunk := range ch {
		content += chunk.Content
		reasoning += chunk.Reasoning
	}

	if reasoning != "je réfléchis donc je suis" {
		t.Fatalf("raisonnement attendu, obtenu %q", reasoning)
	}
	if content != "la réponse" {
		t.Fatalf("contenu attendu, obtenu %q", content)
	}
}

// TestStreamWithToolsContinuesOnToolError : l'échec d'un outil ne doit PAS
// interrompre la réponse (pas de ChatChunk.Err fatal). L'agent doit être
// relancé et produire une réponse dégradée, et les fragments de cycle de vie
// Start/End (End portant l'erreur) doivent être émis.
func TestStreamWithToolsContinuesOnToolError(t *testing.T) {
	client := &failOnceClient{}
	agent := NewChatAgent(client, nil)

	ch, err := agent.streamWithTools(context.Background(),
		[]llm.Message{llm.NewMessage(llm.RoleUser, "cherche")},
		[]llm.Tool{stubTool("", errors.New("serveur MCP injoignable"))}, 256, 3, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	var content string
	var gotErr, sawStart, sawEndWithErr bool
	for chunk := range ch {
		if chunk.Err != nil {
			gotErr = true
		}
		if chunk.Tool != nil {
			switch chunk.Tool.Phase {
			case port.ToolPhaseStart:
				sawStart = true
			case port.ToolPhaseEnd:
				if chunk.Tool.Err != nil {
					sawEndWithErr = true
				}
			}
		}
		content += chunk.Content
	}

	if gotErr {
		t.Fatal("un échec d'outil ne doit pas produire de fragment d'erreur fatale")
	}
	if !sawStart {
		t.Fatal("le fragment ToolPhaseStart n'a pas été émis")
	}
	if !sawEndWithErr {
		t.Fatal("le fragment ToolPhaseEnd portant l'erreur d'outil n'a pas été émis")
	}
	if content != "réponse malgré l'échec de l'outil" {
		t.Fatalf("réponse dégradée attendue, obtenu %q", content)
	}
}
