// Package static embarque les assets statiques (CSS du design system et JS
// applicatif) dans le binaire edecán — pas de dépendance à un chemin relatif
// au démarrage, ni à un CDN externe (conformité CSP, disponibilité hors-ligne).
package static

import "embed"

//go:embed css js
var Files embed.FS
