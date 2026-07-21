// Package build expose les informations de version injectées au moment de la
// compilation via les LDFLAGS (`-X`), cf. Makefile et .goreleaser.yaml.
package build

// ShortVersion est la version courte (ex: tag Git le plus proche).
var ShortVersion = "unknown"

// LongVersion est la version détaillée (tag + nombre de commits + hash + état).
var LongVersion = "unknown"
