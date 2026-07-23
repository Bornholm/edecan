package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

// Seuils de déclenchement du résumé automatique de contexte (SPEC §Chat,
// point 11). Choix arbitraire pour le MVP — à ajuster selon le modèle/
// fournisseur retenu (cf. SPEC §Open Questions, point 3).
const (
	summarizeMinMessages = 20
	summarizeKeepRecent  = 8
)

// ChatService porte les cas d'usage de la conversation (SPEC §Chat).
type ChatService struct {
	sessions   port.SessionRepository
	messages   port.MessageRepository
	flags      port.RelevanceFlagRepository
	projects   map[model.ProjectID]model.Project
	agents     map[model.AgentID]model.Agent
	chatAgents map[model.AgentID]port.ChatAgent
	personas   model.Personas
	// matchEmail teste la correspondance d'un email à un pattern. Injecté par
	// l'appelant (cf. auth.MatchesPattern) pour garder la primitive de
	// correspondance hors du domaine, à l'image de Project.RoleFor.
	matchEmail func(email, pattern string) bool
}

func NewChatService(
	sessions port.SessionRepository,
	messages port.MessageRepository,
	flags port.RelevanceFlagRepository,
	projects map[model.ProjectID]model.Project,
	agents map[model.AgentID]model.Agent,
	chatAgents map[model.AgentID]port.ChatAgent,
	personas model.Personas,
	matchEmail func(email, pattern string) bool,
) *ChatService {
	return &ChatService{
		sessions:   sessions,
		messages:   messages,
		flags:      flags,
		projects:   projects,
		agents:     agents,
		chatAgents: chatAgents,
		personas:   personas,
		matchEmail: matchEmail,
	}
}

func (s *ChatService) projectAgent(projectID model.ProjectID) (model.Project, model.Agent, port.ChatAgent, error) {
	project, ok := s.projects[projectID]
	if !ok {
		return model.Project{}, model.Agent{}, nil, ErrNotFound
	}
	agent, ok := s.agents[project.AgentID]
	if !ok {
		return model.Project{}, model.Agent{}, nil, fmt.Errorf("agent %q introuvable pour le projet %q", project.AgentID, project.ID)
	}
	chatAgent, ok := s.chatAgents[project.AgentID]
	if !ok {
		return model.Project{}, model.Agent{}, nil, fmt.Errorf("agent LLM %q introuvable pour le projet %q", project.AgentID, project.ID)
	}
	return project, agent, chatAgent, nil
}

// StartSession démarre une nouvelle session de chat sur un projet
// (SPEC §Chat, points 5 et 8 : plusieurs sessions parallèles par User/projet).
func (s *ChatService) StartSession(ctx context.Context, projectID model.ProjectID, userID model.UserID, title string) (*model.Session, error) {
	if _, ok := s.projects[projectID]; !ok {
		return nil, ErrNotFound
	}
	now := time.Now()
	sess := &model.Session{
		ProjectID: projectID,
		UserID:    userID,
		Title:     title,
		Status:    model.SessionStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.sessions.Save(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// ListSessions retourne les sessions de userID sur projectID, triées par
// dernière activité (SPEC §Chat, point 9 : reprise de session).
func (s *ChatService) ListSessions(ctx context.Context, projectID model.ProjectID, userID model.UserID) ([]*model.Session, error) {
	return s.sessions.ListByUserAndProject(ctx, userID, projectID)
}

// GetSession retourne la session sessionID si elle appartient à userID.
func (s *ChatService) GetSession(ctx context.Context, sessionID model.SessionID, userID model.UserID) (*model.Session, error) {
	return getOwnedSession(ctx, s.sessions, sessionID, userID)
}

// DeleteSession supprime définitivement une session et son historique
// (messages, signalement éventuel), après vérification de propriété. Une
// session déjà convertie en ticket ne peut pas être supprimée : elle fait
// partie du fil d'audit du ticket (ErrSessionHasTicket) — c'est à l'appelant
// de ne pas proposer l'action dans ce cas (cf. SessionEntry.TicketRef côté
// UI), cette vérification serveur reste l'autorité (SPEC §Sécurité).
func (s *ChatService) DeleteSession(ctx context.Context, sessionID model.SessionID, userID model.UserID) error {
	sess, err := getOwnedSession(ctx, s.sessions, sessionID, userID)
	if err != nil {
		return err
	}
	if sess.HasBeenConvertedToTicket() {
		return ErrSessionHasTicket
	}

	if err := s.messages.DeleteBySession(ctx, sessionID); err != nil {
		return err
	}
	if err := s.flags.DeleteBySession(ctx, sessionID); err != nil {
		return err
	}
	if err := s.sessions.Delete(ctx, sessionID); err != nil {
		return err
	}

	// Libère toute ressource par session de l'agent (ex. connexion MCP,
	// cf. port.ChatAgent.ForgetSession) — best-effort : la session est de
	// toute façon déjà supprimée à ce stade.
	if _, _, chatAgent, err := s.projectAgent(sess.ProjectID); err == nil {
		chatAgent.ForgetSession(fmt.Sprintf("%d", sessionID))
	}

	return nil
}

// ListMessages retourne les messages de la session, après vérification de
// propriété.
func (s *ChatService) ListMessages(ctx context.Context, sessionID model.SessionID, userID model.UserID) ([]*model.Message, error) {
	if _, err := getOwnedSession(ctx, s.sessions, sessionID, userID); err != nil {
		return nil, err
	}
	return s.messages.ListBySession(ctx, sessionID)
}

// PostUserMessage persiste un message User et touche la session
// (SPEC §Chat, point 7).
func (s *ChatService) PostUserMessage(ctx context.Context, sessionID model.SessionID, userID model.UserID, content string) (*model.Message, error) {
	sess, err := getOwnedSession(ctx, s.sessions, sessionID, userID)
	if err != nil {
		return nil, err
	}

	msg := &model.Message{SessionID: sessionID, Role: model.MessageRoleUser, Content: content, CreatedAt: time.Now()}
	if err := s.messages.Save(ctx, msg); err != nil {
		return nil, err
	}

	sess.UpdatedAt = time.Now()
	if err := s.sessions.Save(ctx, sess); err != nil {
		return nil, err
	}
	return msg, nil
}

// llmContext retourne le contexte à transmettre au LLM : si un résumé a déjà
// été généré, seuls le résumé et les messages postérieurs sont inclus — le
// reste de l'historique demeure persisté et consultable, seul le prompt
// envoyé au LLM est réduit (SPEC §Chat, point 11).
func (s *ChatService) llmContext(ctx context.Context, sessionID model.SessionID) ([]model.Message, error) {
	all, err := s.messages.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	lastSummary := -1
	for i, m := range all {
		if m.Role == model.MessageRoleSummary {
			lastSummary = i
		}
	}

	history := make([]model.Message, 0, len(all))
	start := 0
	if lastSummary >= 0 {
		start = lastSummary
	}
	for _, m := range all[start:] {
		history = append(history, *m)
	}
	return history, nil
}

// StreamAssistantReply génère, en la streamant, la réponse de l'agent pour la
// session — résumant si besoin le contexte avant de poursuivre (SPEC §Chat,
// points 6, 10, 11). L'appelant DOIT consommer le channel jusqu'à fermeture
// ou annulation de ctx (cf. PLAN.md §Phase 4) puis appeler FinalizeReply.
func (s *ChatService) StreamAssistantReply(ctx context.Context, sessionID model.SessionID, user *model.User) (<-chan port.ChatChunk, error) {
	sess, err := getOwnedSession(ctx, s.sessions, sessionID, user.ID)
	if err != nil {
		return nil, err
	}

	_, agent, chatAgent, err := s.projectAgent(sess.ProjectID)
	if err != nil {
		return nil, err
	}

	// Applique les personas correspondant à l'utilisateur pour ce projet :
	// leur contexte enrichit le prompt système et leurs serveurs MCP s'ajoutent
	// à ceux de l'agent (agent est une copie — sûr à muter ; MergeMCPServers ne
	// mute pas le slice partagé de l'agent d'origine).
	if s.matchEmail != nil {
		matched := s.personas.Resolve(sess.ProjectID, func(pattern string) bool {
			return s.matchEmail(user.Email, pattern)
		})
		agent.SystemPrompt = model.AugmentSystemPrompt(agent.SystemPrompt, matched.Prompts())
		agent.MCPServers = model.MergeMCPServers(agent.MCPServers, matched.MCPServers())
	}

	ctx = port.WithMCPIdentity(ctx, port.MCPIdentity{
		SessionID:       fmt.Sprintf("%d", sessionID),
		UserEmail:       user.Email,
		UserDisplayName: user.DisplayName,
	})

	history, err := s.llmContext(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	// Le déclenchement du résumé se mesure sur les seuls échanges : compter les
	// tours d'outils y ferait basculer une conversation courte dès qu'elle
	// mobilise beaucoup d'outils, alors que le résumé porte sur les messages.
	conversation := model.ConversationOnly(history)
	if len(conversation) > summarizeMinMessages {
		older := conversation[:len(conversation)-summarizeKeepRecent]
		summaryText, err := chatAgent.Summarize(ctx, agent, older)
		if err == nil && summaryText != "" {
			summaryTime := older[len(older)-1].CreatedAt.Add(time.Millisecond)
			summaryMsg := &model.Message{SessionID: sessionID, Role: model.MessageRoleSummary, Content: summaryText, CreatedAt: summaryTime}
			if err := s.messages.Save(ctx, summaryMsg); err == nil {
				history, err = s.llmContext(ctx, sessionID)
				if err != nil {
					return nil, err
				}
			}
		}
		// Échec de résumé : on poursuit avec l'historique complet plutôt que
		// de bloquer la conversation (dégradé acceptable).
	}

	return chatAgent.StreamReply(ctx, agent, history)
}

// FinalizeReply persiste la réponse complète de l'agent une fois le
// streaming terminé. reasoning porte le raisonnement éventuel exposé par le
// modèle (vide sinon) — stocké à part du contenu, pour affichage seulement.
//
// toolTurns porte les allers-retours outil↔LLM résolus pendant la génération
// (cf. port.ChatChunk.ToolTurn), persistés avant la réponse et dans leur ordre
// d'apparition : c'est ce qui permet à l'agent de retrouver, aux tours
// suivants, ce que ses outils ont réellement retourné. Ils ne sont enregistrés
// qu'ici, avec la réponse : une génération interrompue ne laisse donc aucune
// trace partielle, et le retry repart d'un historique propre.
func (s *ChatService) FinalizeReply(ctx context.Context, sessionID model.SessionID, content, reasoning string, toolTurns []model.Message) error {
	for i := range toolTurns {
		turn := toolTurns[i]
		turn.SessionID = sessionID
		turn.CreatedAt = time.Now()
		if err := s.messages.Save(ctx, &turn); err != nil {
			// Perdre un tour d'outil dégrade la mémoire de l'agent mais ne doit
			// pas coûter la réponse elle-même : on journalise et on poursuit.
			slog.ErrorContext(ctx, "persistance d'un tour d'outil", "session_id", sessionID, "error", err)
		}
	}

	msg := &model.Message{SessionID: sessionID, Role: model.MessageRoleAssistant, Content: content, Reasoning: reasoning, CreatedAt: time.Now()}
	if err := s.messages.Save(ctx, msg); err != nil {
		return err
	}

	sess, err := s.sessions.FindByID(ctx, sessionID)
	if err != nil || sess == nil {
		return err
	}
	sess.UpdatedAt = time.Now()
	return s.sessions.Save(ctx, sess)
}

// Project retourne le projet par ID, pour les besoins d'affichage (titre,
// rôle…) côté handler.
func (s *ChatService) Project(projectID model.ProjectID) (model.Project, bool) {
	p, ok := s.projects[projectID]
	return p, ok
}

// LastVisitedProject retourne le projet de la session la plus récemment
// active de userID, tous projets confondus — utilisé pour reprendre
// l'utilisateur sur son dernier projet à la connexion. ok est faux si
// l'utilisateur n'a encore aucune session.
func (s *ChatService) LastVisitedProject(ctx context.Context, userID model.UserID) (model.ProjectID, bool, error) {
	sess, err := s.sessions.FindMostRecentByUser(ctx, userID)
	if err != nil {
		return "", false, err
	}
	if sess == nil {
		return "", false, nil
	}
	return sess.ProjectID, true, nil
}
