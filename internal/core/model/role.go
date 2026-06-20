package model

// Role est le rôle d'un utilisateur au sein d'un projet (SPEC §Authentification).
type Role string

const (
	RoleUser    Role = "user"
	RoleSupport Role = "support"
)

// HigherOrEqual indique si r est au moins aussi élevé que other.
// Support > User — en cas de correspondances multiples, le rôle le plus
// élevé gagne (cf. SPEC §Authentification, point 3).
func (r Role) HigherOrEqual(other Role) bool {
	rank := map[Role]int{RoleUser: 0, RoleSupport: 1}
	return rank[r] >= rank[other]
}

// HighestRole retourne le rôle le plus élevé parmi roles. Panique si roles
// est vide — un appelant ne doit jamais invoquer cette fonction sans au
// moins une correspondance d'appartenance.
func HighestRole(roles []Role) Role {
	highest := RoleUser
	for _, r := range roles {
		if r.HigherOrEqual(highest) {
			highest = r
		}
	}
	return highest
}
