// Package component porte les composants du Design System edecàn
// (Design system Edecàn/components/*.jsx) en composants templ, stylés via
// les classes définies dans internal/http/static/css/components.css.
package component

import "strings"

// classes assemble une liste de classes CSS en ignorant les chaînes vides —
// pratique pour composer des classes conditionnelles sans templ.Classes.
func classes(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " ")
}

// ifClass retourne class si cond est vrai, une chaîne vide sinon — à
// combiner avec classes() pour des classes conditionnelles.
func ifClass(cond bool, class string) string {
	if cond {
		return class
	}
	return ""
}
