package llm

import (
	"context"
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

// stubClient implémente llm.Client. Tant que les outils sont autorisés
// (ToolChoice != none) il redemande un appel d'outil "search" ; dès que
// l'appelant interdit les outils (ToolChoiceNone), il renvoie une réponse
// textuelle. Il compte les complétions et mémorise la dernière ToolChoice.
type stubClient struct {
	completions    int
	autoCalls      int
	lastToolChoice llm.ToolChoice
}

func (c *stubClient) ChatCompletion(ctx context.Context, funcs ...llm.ChatCompletionOptionFunc) (llm.ChatCompletionResponse, error) {
	opts := &llm.ChatCompletionOptions{}
	for _, fn := range funcs {
		fn(opts)
	}
	c.completions++
	c.lastToolChoice = opts.ToolChoice

	if opts.ToolChoice == llm.ToolChoiceNone {
		return stubResponse{content: "réponse finale à partir du contexte"}, nil
	}
	c.autoCalls++
	return stubResponse{toolCalls: []llm.ToolCall{llm.NewToolCall("id", "search", "{}")}}, nil
}

func (c *stubClient) ChatCompletionStream(ctx context.Context, funcs ...llm.ChatCompletionOptionFunc) (<-chan llm.StreamChunk, error) {
	panic("non utilisé")
}

func (c *stubClient) Embeddings(ctx context.Context, inputs []string, funcs ...llm.EmbeddingsOptionFunc) (llm.EmbeddingsResponse, error) {
	panic("non utilisé")
}

func searchTool() llm.Tool {
	return llm.NewFuncTool("search", "recherche", map[string]any{},
		func(ctx context.Context, params map[string]any) (llm.ToolResult, error) {
			return llm.NewToolResult("aucun résultat"), nil
		})
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
		[]llm.Tool{searchTool()}, 256, maxIterations)
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
	// + 1 complétion finale forcée en ToolChoiceNone.
	if client.autoCalls != maxIterations {
		t.Fatalf("attendu %d appels auto, obtenu %d", maxIterations, client.autoCalls)
	}
	if client.completions != maxIterations+1 {
		t.Fatalf("attendu %d complétions au total, obtenu %d", maxIterations+1, client.completions)
	}
	if client.lastToolChoice != llm.ToolChoiceNone {
		t.Fatalf("la dernière complétion doit interdire les outils, ToolChoice=%q", client.lastToolChoice)
	}
}
