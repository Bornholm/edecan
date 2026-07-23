package llm

import (
	"encoding/json"

	"github.com/bornholm/genai/llm"

	"edecan/internal/core/model"
)

// emptyToolArguments est la valeur de repli lorsqu'un appel d'outil n'a pas
// d'arguments exploitables : les providers attendent un objet JSON valide,
// pas une chaîne vide.
const emptyToolArguments = "{}"

// toolCallsRecord projette les appels d'outils décidés par le modèle en un
// message du domaine persistable. SessionID et CreatedAt sont laissés à zéro :
// c'est la couche service qui les renseigne au moment de la persistance
// (cf. service.ChatService.FinalizeReply).
func toolCallsRecord(toolCalls []llm.ToolCall) model.Message {
	calls := make([]model.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		calls = append(calls, model.ToolCall{
			ID:        tc.ID(),
			Name:      tc.Name(),
			Arguments: toolArguments(tc.Parameters()),
		})
	}
	return model.Message{Role: model.MessageRoleAssistant, ToolCalls: calls}
}

// toolResultRecord projette le résultat d'un appel d'outil en message du
// domaine. L'identifiant provient de l'appel plutôt que du message d'outil :
// c'est celui que le modèle a émis, donc celui qui doit permettre l'appariement
// lors du rejeu (cf. toLLMMessages).
func toolResultRecord(tc llm.ToolCall, toolMsg llm.ToolMessage) model.Message {
	return model.Message{
		Role:       model.MessageRoleTool,
		Content:    toolMsg.Content(),
		ToolCallID: tc.ID(),
		ToolName:   tc.Name(),
	}
}

// toolArguments normalise les paramètres d'un appel d'outil en JSON. Les
// providers exposent en principe la chaîne JSON brute produite par le modèle
// (cf. genai/llm/provider/openai) — on la conserve telle quelle pour un
// aller-retour fidèle, et on sérialise en dernier recours.
func toolArguments(params any) string {
	switch v := params.(type) {
	case nil:
		return emptyToolArguments
	case string:
		if v == "" {
			return emptyToolArguments
		}
		return v
	case []byte:
		if len(v) == 0 {
			return emptyToolArguments
		}
		return string(v)
	}

	raw, err := json.Marshal(params)
	if err != nil {
		return emptyToolArguments
	}
	return string(raw)
}
