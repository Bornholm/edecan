package llm

import (
	"context"
	"testing"

	"github.com/bornholm/genai/llm"

	"edecan/internal/core/model"
)

// toolTurnHistory construit un historique persisté représentant un tour
// d'outil complet : question, appel de l'outil, résultat, puis réponse.
func toolTurnHistory() []model.Message {
	return []model.Message{
		{Role: model.MessageRoleUser, Content: "quelles règles s'appliquent ?"},
		{Role: model.MessageRoleAssistant, ToolCalls: []model.ToolCall{
			{ID: testToolCallID, Name: testToolName, Arguments: `{"q":"code postal"}`},
		}},
		{Role: model.MessageRoleTool, ToolCallID: testToolCallID, ToolName: testToolName, Content: "extrait du fichier ProfileType.php"},
		{Role: model.MessageRoleAssistant, Content: "le champ est conditionnel"},
	}
}

// TestToLLMMessagesReplaysToolTurn : un tour d'outil persisté doit être rejoué
// tel quel dans le contexte du modèle — c'est ce qui donne à l'agent la mémoire
// de ses propres recherches aux tours suivants.
func TestToLLMMessagesReplaysToolTurn(t *testing.T) {
	messages := toLLMMessages("consigne système", toolTurnHistory())

	if len(messages) != 5 {
		t.Fatalf("attendu 5 messages (système + 4), obtenu %d", len(messages))
	}

	callsMsg, ok := messages[2].(llm.ToolCallsMessage)
	if !ok {
		t.Fatalf("message d'appels d'outils attendu en position 2, obtenu %T", messages[2])
	}
	calls := callsMsg.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("attendu 1 appel d'outil, obtenu %d", len(calls))
	}
	if calls[0].ID() != testToolCallID || calls[0].Name() != testToolName {
		t.Fatalf("appel mal restitué: id=%q name=%q", calls[0].ID(), calls[0].Name())
	}
	if params, _ := calls[0].Parameters().(string); params != `{"q":"code postal"}` {
		t.Fatalf("arguments mal restitués: %v", calls[0].Parameters())
	}

	toolMsg, ok := messages[3].(llm.ToolMessage)
	if !ok {
		t.Fatalf("message d'outil attendu en position 3, obtenu %T", messages[3])
	}
	if toolMsg.ID() != testToolCallID {
		t.Fatalf("identifiant d'appariement attendu %q, obtenu %q", testToolCallID, toolMsg.ID())
	}
	if toolMsg.Content() != "extrait du fichier ProfileType.php" {
		t.Fatalf("résultat d'outil mal restitué: %q", toolMsg.Content())
	}
}

// TestToLLMMessagesDropsOrphanToolResult : un résultat dont l'appel a disparu
// de la fenêtre de contexte (historique tronqué au milieu d'un tour) doit être
// écarté — le laisser ferait rejeter la requête par le provider.
func TestToLLMMessagesDropsOrphanToolResult(t *testing.T) {
	// Historique tronqué : le message d'appels a été coupé.
	truncated := toolTurnHistory()[2:]

	messages := toLLMMessages("", truncated)

	if len(messages) != 1 {
		t.Fatalf("attendu le seul message conversationnel, obtenu %d messages", len(messages))
	}
	if _, ok := messages[0].(llm.ToolMessage); ok {
		t.Fatal("le résultat d'outil orphelin ne doit pas être rejoué")
	}
}

// TestToLLMMessagesDropsCallWithoutResult : symétriquement, un appel dont le
// résultat manque ne doit pas être rejoué — l'invariant « une réponse par
// appel » doit tenir dans les deux sens.
func TestToLLMMessagesDropsCallWithoutResult(t *testing.T) {
	history := toolTurnHistory()
	// Retire le résultat, en conservant l'appel.
	history = append(history[:2], history[3])

	messages := toLLMMessages("", history)

	for _, m := range messages {
		if _, ok := m.(llm.ToolCallsMessage); ok {
			t.Fatal("l'appel d'outil sans résultat ne doit pas être rejoué")
		}
	}
	if len(messages) != 2 {
		t.Fatalf("attendu les 2 messages conversationnels, obtenu %d", len(messages))
	}
}

// TestStreamWithToolsEmitsToolTurn : un aller-retour outil↔LLM résolu doit être
// remonté à l'appelant sous forme de messages persistables (appels puis
// résultats), sans quoi rien ne serait enregistré et l'agent oublierait sa
// recherche dès le message suivant.
func TestStreamWithToolsEmitsToolTurn(t *testing.T) {
	agent := NewChatAgent(&failOnceClient{})

	ch, err := agent.streamWithTools(context.Background(),
		[]llm.Message{llm.NewMessage(llm.RoleUser, "cherche")},
		[]llm.Tool{stubTool("extrait pertinent", nil)}, 256, 3, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	var turns []model.Message
	for chunk := range ch {
		turns = append(turns, chunk.ToolTurn...)
	}

	if len(turns) != 2 {
		t.Fatalf("attendu 2 messages de tour d'outil (appels + résultat), obtenu %d", len(turns))
	}

	callsMsg := turns[0]
	if callsMsg.Role != model.MessageRoleAssistant || len(callsMsg.ToolCalls) != 1 {
		t.Fatalf("message d'appels attendu, obtenu %+v", callsMsg)
	}
	if callsMsg.ToolCalls[0].ID != testToolCallID || callsMsg.ToolCalls[0].Name != testToolName {
		t.Fatalf("appel mal projeté: %+v", callsMsg.ToolCalls[0])
	}
	if callsMsg.ToolCalls[0].Arguments != "{}" {
		t.Fatalf("arguments attendus %q, obtenu %q", "{}", callsMsg.ToolCalls[0].Arguments)
	}

	resultMsg := turns[1]
	if resultMsg.Role != model.MessageRoleTool {
		t.Fatalf("message de résultat attendu, obtenu le rôle %q", resultMsg.Role)
	}
	if resultMsg.ToolCallID != testToolCallID || resultMsg.ToolName != testToolName {
		t.Fatalf("appariement du résultat incorrect: %+v", resultMsg)
	}
	if resultMsg.Content != "extrait pertinent" {
		t.Fatalf("contenu du résultat attendu, obtenu %q", resultMsg.Content)
	}
}

// TestStreamWithToolsEmitsToolTurnOnFailure : même en cas d'échec de l'outil, le
// tour doit être remonté — l'agent a besoin de savoir, aux tours suivants,
// qu'il a tenté la recherche et pourquoi elle n'a rien donné.
func TestStreamWithToolsEmitsToolTurnOnFailure(t *testing.T) {
	agent := NewChatAgent(&failOnceClient{})

	ch, err := agent.streamWithTools(context.Background(),
		[]llm.Message{llm.NewMessage(llm.RoleUser, "cherche")},
		[]llm.Tool{stubTool("", context.DeadlineExceeded)}, 256, 3, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	var turns []model.Message
	for chunk := range ch {
		turns = append(turns, chunk.ToolTurn...)
	}

	if len(turns) != 2 {
		t.Fatalf("attendu 2 messages de tour d'outil, obtenu %d", len(turns))
	}
	if turns[1].Role != model.MessageRoleTool || turns[1].Content == "" {
		t.Fatalf("le résultat d'échec doit être persistable et non vide: %+v", turns[1])
	}
}

// TestConversationOnlyStripsToolTurns : les usages qui raisonnent sur les
// échanges (résumé, brouillon de ticket) ne doivent voir que la conversation.
func TestConversationOnlyStripsToolTurns(t *testing.T) {
	conversation := model.ConversationOnly(toolTurnHistory())

	if len(conversation) != 2 {
		t.Fatalf("attendu 2 messages conversationnels, obtenu %d", len(conversation))
	}
	for _, m := range conversation {
		if m.IsToolTurn() {
			t.Fatalf("tour d'outil non filtré: %+v", m)
		}
	}
}

// TestToolArgumentsFallback : des paramètres absents ou vides doivent être
// normalisés en objet JSON valide, seule forme acceptée par les providers.
func TestToolArgumentsFallback(t *testing.T) {
	for _, params := range []any{nil, "", []byte(nil)} {
		if got := toolArguments(params); got != emptyToolArguments {
			t.Fatalf("pour %#v, attendu %q, obtenu %q", params, emptyToolArguments, got)
		}
	}
	if got := toolArguments(map[string]any{"q": "x"}); got != `{"q":"x"}` {
		t.Fatalf("sérialisation de repli inattendue: %q", got)
	}
}

// TestStreamPlainEmitsNoToolTurn : sans outil, aucun tour ne doit être remonté.
func TestStreamPlainEmitsNoToolTurn(t *testing.T) {
	agent := NewChatAgent(&reasoningClient{})

	ch, err := agent.streamWithTools(context.Background(),
		[]llm.Message{llm.NewMessage(llm.RoleUser, "coucou")},
		[]llm.Tool{stubTool("inutilisé", nil)}, 256, 3, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	for chunk := range ch {
		if len(chunk.ToolTurn) > 0 {
			t.Fatalf("aucun tour d'outil attendu, obtenu %d messages", len(chunk.ToolTurn))
		}
	}
}
