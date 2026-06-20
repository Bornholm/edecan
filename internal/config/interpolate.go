package config

import (
	"fmt"
	"os"
	"regexp"
)

// envPattern reconnaît ${VAR_NAME} et ${VAR_NAME:-default}.
var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// interpolateEnv remplace les références ${VAR} par leur valeur d'environnement.
// Une variable sans valeur par défaut et absente de l'environnement provoque une
// erreur fail-fast (cf. SPEC §Edge Cases : config malformée → échec au chargement).
func interpolateEnv(raw []byte) ([]byte, error) {
	var firstErr error

	result := envPattern.ReplaceAllFunc(raw, func(match []byte) []byte {
		sub := envPattern.FindSubmatch(match)
		name := string(sub[1])
		hasDefault := len(sub[2]) > 0
		defaultValue := string(sub[3])

		if value, ok := os.LookupEnv(name); ok {
			return []byte(value)
		}
		if hasDefault {
			return []byte(defaultValue)
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("variable d'environnement %q référencée dans la configuration mais non définie", name)
		}
		return match
	})

	if firstErr != nil {
		return nil, firstErr
	}
	return result, nil
}
