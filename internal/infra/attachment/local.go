package attachment

import (
	"context"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"edecan/internal/core/port"
)

// LocalStore implémente port.AttachmentStore sur un répertoire local.
type LocalStore struct {
	rootDir string
}

// NewLocalStore construit un LocalStore enraciné sous rootDir, créé s'il
// n'existe pas.
func NewLocalStore(rootDir string) (*LocalStore, error) {
	if err := os.MkdirAll(rootDir, 0o700); err != nil {
		return nil, fmt.Errorf("création du répertoire de stockage des pièces jointes: %w", err)
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("résolution du répertoire de stockage des pièces jointes: %w", err)
	}
	return &LocalStore{rootDir: abs}, nil
}

var _ port.AttachmentStore = (*LocalStore)(nil)

// Save écrit content sous {rootDir}/{aléatoire}/{nom assaini} — voir
// port.AttachmentStore.Save.
func (s *LocalStore) Save(ctx context.Context, filename string, content io.Reader) (string, string, int64, error) {
	name := sanitizeFilename(filename)
	relPath := pathpkg.Join(randomToken(), name)
	absPath := filepath.Join(s.rootDir, relPath)

	if err := os.MkdirAll(filepath.Dir(absPath), 0o700); err != nil {
		return "", "", 0, fmt.Errorf("création du répertoire de la pièce jointe: %w", err)
	}

	f, err := os.OpenFile(absPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", "", 0, fmt.Errorf("création du fichier de la pièce jointe: %w", err)
	}
	defer f.Close()

	size, err := io.Copy(f, content)
	if err != nil {
		return "", "", 0, fmt.Errorf("écriture de la pièce jointe: %w", err)
	}

	return encodeRef(relPath), name, size, nil
}

// Open relit le fichier référencé par ref — voir port.AttachmentStore.Open.
func (s *LocalStore) Open(ctx context.Context, ref string) (io.ReadCloser, string, error) {
	relPath, err := decodeRef(ref)
	if err != nil {
		return nil, "", err
	}

	absPath := filepath.Join(s.rootDir, relPath)
	// Défense en profondeur : ref est générée par Save et jamais saisie
	// librement par un utilisateur, mais on s'assure malgré tout que le
	// chemin résolu reste sous rootDir avant toute lecture.
	if !strings.HasPrefix(absPath, s.rootDir+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("référence de pièce jointe hors du répertoire de stockage")
	}

	f, err := os.Open(absPath)
	if err != nil {
		return nil, "", fmt.Errorf("lecture de la pièce jointe: %w", err)
	}

	return f, pathpkg.Base(relPath), nil
}
