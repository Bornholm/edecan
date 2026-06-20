package auth

import (
	"path"
	"strings"

	"edecan/internal/core/model"
)

// MatchesPattern indique si email correspond à pattern. Le pattern supporte
// les jokers `*` et `?` (ex: "*@exemple.com") en plus de la correspondance
// exacte (SPEC §Authentification, point 2).
func MatchesPattern(email, pattern string) bool {
	matched, err := path.Match(pattern, strings.ToLower(email))
	if err != nil {
		return false
	}
	return matched
}

// ResolveRole détermine le rôle d'un email pour un projet. ok est false si
// l'utilisateur n'est membre d'aucune règle (donc pas membre du projet).
// En cas de correspondances multiples, le rôle le plus élevé gagne
// (Support > User — SPEC §Authentification, point 3).
func ResolveRole(project model.Project, email string) (role model.Role, ok bool) {
	return project.RoleFor(func(pattern string) bool {
		return MatchesPattern(email, pattern)
	})
}
