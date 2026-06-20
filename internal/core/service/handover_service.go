package service

import (
	"context"
	"log/slog"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
)

// HandoverService transforme une session de chat en ticket
// (SPEC §Glossaire : Handover ; PLAN.md §Phase 5).
type HandoverService struct {
	sessions   port.SessionRepository
	messages   port.MessageRepository
	projects   map[model.ProjectID]model.Project
	agents     map[model.AgentID]model.Agent
	chatAgents map[model.AgentID]port.ChatAgent
	tickets    *TicketService
}

func NewHandoverService(
	sessions port.SessionRepository,
	messages port.MessageRepository,
	projects map[model.ProjectID]model.Project,
	agents map[model.AgentID]model.Agent,
	chatAgents map[model.AgentID]port.ChatAgent,
	tickets *TicketService,
) *HandoverService {
	return &HandoverService{
		sessions:   sessions,
		messages:   messages,
		projects:   projects,
		agents:     agents,
		chatAgents: chatAgents,
		tickets:    tickets,
	}
}

// GenerateDraft génère, via le LLM, un brouillon de ticket (titre + corps
// Markdown) à partir des échanges de la session — éditable par le User avant
// soumission (SPEC §Handover, points 14-15).
func (s *HandoverService) GenerateDraft(ctx context.Context, sessionID model.SessionID, userID model.UserID) (title string, body string, err error) {
	sess, err := getOwnedSession(ctx, s.sessions, sessionID, userID)
	if err != nil {
		return "", "", err
	}

	project, ok := s.projects[sess.ProjectID]
	if !ok {
		return "", "", ErrNotFound
	}
	agent, ok := s.agents[project.AgentID]
	if !ok {
		return "", "", ErrNotFound
	}
	chatAgent, ok := s.chatAgents[project.AgentID]
	if !ok {
		return "", "", ErrNotFound
	}

	history, err := s.messages.ListBySession(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	full := make([]model.Message, 0, len(history))
	for _, m := range history {
		full = append(full, *m)
	}

	title, body, err = chatAgent.DraftTicket(ctx, agent, full)
	if err != nil {
		// Brouillon vide éditable + erreur non bloquante
		// (SPEC §Edge Cases : échec de génération du brouillon).
		return "", "", err
	}
	return title, body, nil
}

// Submit crée le ticket à partir du brouillon validé/édité par le User, et
// lie la session au ticket créé — la session reste éditable
// (SPEC §Handover, points 16, 18-19).
func (s *HandoverService) Submit(ctx context.Context, sessionID model.SessionID, requester *model.User, title, body string) (*model.Ticket, error) {
	sess, err := getOwnedSession(ctx, s.sessions, sessionID, requester.ID)
	if err != nil {
		return nil, err
	}

	ticket, _, err := s.tickets.CreateDirect(ctx, sess.ProjectID, requester, title, body, &sessionID)
	if err != nil {
		return nil, err
	}

	sess.LinkTicket(ticket.Ref)
	if err := s.sessions.Save(ctx, sess); err != nil {
		// Ticket créé mais lien non sauvé : dégradé acceptable, à réconcilier
		// (cf. PLAN.md §Phase 5, stratégie à décider).
		slog.ErrorContext(ctx, "handover: ticket créé mais session non liée",
			"ticket_ref", ticket.Ref, "session_id", sessionID, "error", err)
		return ticket, nil
	}
	return ticket, nil
}
