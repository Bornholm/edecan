// Package llm adapte github.com/bornholm/genai au port.ChatAgent du domaine
// (SPEC §Dépendances techniques ; PLAN.md §Phase 4).
package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/bornholm/genai/llm"
	"github.com/bornholm/genai/llm/circuitbreaker"
	"github.com/bornholm/genai/llm/provider"
	"github.com/bornholm/genai/llm/provider/mistral"
	"github.com/bornholm/genai/llm/provider/openai"
	"github.com/bornholm/genai/llm/provider/openrouter"
	"github.com/bornholm/genai/llm/ratelimit"
	"github.com/bornholm/genai/llm/retry"
	"github.com/bornholm/genai/llm/tokenlimit"

	"edecan/internal/core/model"
)

// NewClient construit un llm.Client pour le provider déclaré par l'agent.
// Providers supportés : openai, openrouter, mistral (cf. AGENT.md de genai).
func NewClient(ctx context.Context, agent model.Agent, apiKey string) (llm.Client, error) {
	common := provider.CommonOptions{Model: agent.Model, BaseURL: agent.BaseURL, APIKey: apiKey}

	var optFunc provider.OptionFunc
	switch agent.Provider {
	case "openai":
		optFunc = provider.WithChatCompletion(openai.Name, openai.Options{CommonOptions: common})
	case "openrouter":
		optFunc = provider.WithChatCompletion(openrouter.Name, openrouter.Options{CommonOptions: common})
	case "mistral":
		optFunc = provider.WithChatCompletion(mistral.Name, mistral.Options{CommonOptions: common})
	default:
		return nil, fmt.Errorf("provider LLM %q non supporté", agent.Provider)
	}

	client, err := provider.Create(ctx, optFunc)
	if err != nil {
		return nil, fmt.Errorf("création du client LLM (provider %q): %w", agent.Provider, err)
	}
	return wrapResilient(client), nil
}

// wrapResilient enveloppe client de couches de résilience (cf. genai
// examples/resilient) : retries sur les erreurs transitoires (429, 5xx),
// limitation de débit en requêtes et en tokens, puis circuit breaker pour
// échouer rapidement si le provider est durablement indisponible.
func wrapResilient(client llm.Client) llm.Client {
	retryClient := retry.NewClient(client, time.Second, 3)
	rateLimitedClient := ratelimit.NewClient(retryClient,
		ratelimit.WithChatLimit(time.Minute/60, 5),
		ratelimit.WithEmbeddingsLimit(time.Minute/60, 5),
	)
	tokenRateLimitedClient := tokenlimit.NewClient(rateLimitedClient)
	return circuitbreaker.NewClient(tokenRateLimitedClient, 5, 30*time.Second)
}
