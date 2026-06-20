// Package static embarque les assets statiques (CSS du design system) dans
// le binaire edecán — pas de dépendance à un chemin relatif au démarrage.
package static

import "embed"

//go:embed css
var Files embed.FS
