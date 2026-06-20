// Package markdown rend en HTML le contenu Markdown des messages, brouillons
// de ticket et commentaires (SPEC §Chat point 12 ; §Handover ; §Tickets).
package markdown

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// renderer active l'extension GFM (tables, texte barré, listes de tâches,
// auto-liens) : les agents produisent couramment des tables au format GFM
// (ex. codes d'erreur), que goldmark seul aplatit en un simple paragraphe.
//
// Il n'active PAS goldmark.WithUnsafe() : tout HTML brut présent dans la
// source Markdown est échappé plutôt qu'injecté tel quel. C'est la seule
// défense XSS pour du contenu produit par un User ou par le LLM
// (cf. consignes sécurité : éviter toute vulnérabilité XSS).
var renderer = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
)

// ToHTML convertit du Markdown en HTML sûr à insérer via templ.Raw.
func ToHTML(source string) (string, error) {
	var buf bytes.Buffer
	if err := renderer.Convert([]byte(source), &buf); err != nil {
		return "", fmt.Errorf("rendu markdown: %w", err)
	}
	return buf.String(), nil
}
