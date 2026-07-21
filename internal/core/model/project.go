package model

// MembershipRule associe un pattern d'email à un rôle pour un projet
// (SPEC §Authentification, point 2 et 3).
type MembershipRule struct {
	Pattern string
	Role    Role
}

// Project est un espace de support configuré, associé à un agent et des
// règles d'appartenance utilisateur. Le backend de tickets est optionnel :
// laissé vide, le projet ne propose que l'interface de chat (pas de tickets
// ni de handover) — cf. HasTicketBackend.
// Les projets sont définis dans la configuration YAML — ce ne sont pas des
// entités persistées en base (cf. SPEC §Configuration).
type Project struct {
	ID            ProjectID
	Name          string
	AgentID       AgentID
	TicketBackend TicketBackendID
	Membership    []MembershipRule
}

// HasTicketBackend indique si le projet est adossé à un backend de tickets.
// Un projet sans backend est « chat-only » : toute l'UI et les routes liées
// aux tickets (liste, détail, handover) sont désactivées pour lui.
func (p Project) HasTicketBackend() bool {
	return p.TicketBackend != ""
}

// RoleFor résout le rôle d'un email pour ce projet. ok est false si aucune
// règle ne correspond (l'utilisateur n'est pas membre du projet).
func (p Project) RoleFor(matches func(pattern string) bool) (role Role, ok bool) {
	var roles []Role
	for _, rule := range p.Membership {
		if matches(rule.Pattern) {
			roles = append(roles, rule.Role)
		}
	}
	if len(roles) == 0 {
		return "", false
	}
	return HighestRole(roles), true
}
