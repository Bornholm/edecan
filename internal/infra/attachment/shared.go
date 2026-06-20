// Package attachment fournit des implémentations de port.AttachmentStore —
// stockage du contenu des pièces jointes hors du backend de tickets
// (cf. internal/core/port.AttachmentStore).
package attachment

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
)

// randomToken génère un composant aléatoire pour rendre deux clés
// distinctes même pour un même nom de fichier déposé deux fois — sans
// dépendance uuid (même principe que internal/auth/cookiestore.go).
func randomToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Errorf("génération de l'aléatoire pour la pièce jointe: %w", err))
	}
	return hex.EncodeToString(buf)
}

// sanitizeFilename ne retient que le nom de fichier final fourni par
// l'utilisateur — un nom de fichier non maîtrisé ne doit jamais influencer
// la clé de stockage (ex. traversée de répertoire via "../").
func sanitizeFilename(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || name == "." || name == ".." {
		return "fichier"
	}
	return name
}

// encodeRef encode key (un chemin/clé pouvant contenir des "/") en une
// référence opaque tenant sur un seul segment d'URL — nécessaire car les
// routes d'edecán ne capturent qu'un segment pour l'identifiant de pièce
// jointe (cf. cmd/edecan/main.go, ".../attachments/{id}").
func encodeRef(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

// decodeRef inverse encodeRef.
func decodeRef(ref string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(ref)
	if err != nil {
		return "", fmt.Errorf("référence de pièce jointe invalide: %w", err)
	}
	return string(raw), nil
}
