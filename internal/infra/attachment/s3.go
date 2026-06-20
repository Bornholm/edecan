package attachment

import (
	"context"
	"fmt"
	pathpkg "path"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"io"

	"edecan/internal/core/port"
)

// S3Config regroupe les paramètres de connexion à un stockage objet
// compatible S3 (AWS S3, MinIO, ou toute autre implémentation compatible).
type S3Config struct {
	Endpoint  string // ex. "s3.amazonaws.com" ou "minio.interne:9000" — sans schéma
	Bucket    string
	Prefix    string // préfixe de clé optionnel, pour partager un bucket entre usages
	Region    string
	UseSSL    bool
	AccessKey string
	SecretKey string
}

// S3Store implémente port.AttachmentStore sur un stockage objet compatible S3.
type S3Store struct {
	client *minio.Client
	bucket string
	prefix string
}

// NewS3Store construit un S3Store prêt à l'emploi.
func NewS3Store(cfg S3Config) (*S3Store, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("création du client S3: %w", err)
	}
	return &S3Store{client: client, bucket: cfg.Bucket, prefix: cfg.Prefix}, nil
}

var _ port.AttachmentStore = (*S3Store)(nil)

// Save dépose content sous {prefix}/{aléatoire}/{nom assaini} — voir
// port.AttachmentStore.Save.
func (s *S3Store) Save(ctx context.Context, filename string, content io.Reader) (string, string, int64, error) {
	name := sanitizeFilename(filename)
	key := pathpkg.Join(s.prefix, randomToken(), name)

	// objectSize = -1 : taille inconnue à l'avance, minio bascule alors sur
	// un upload multipart streamé plutôt que d'exiger une taille fixe.
	info, err := s.client.PutObject(ctx, s.bucket, key, content, -1, minio.PutObjectOptions{})
	if err != nil {
		return "", "", 0, fmt.Errorf("dépôt de la pièce jointe sur le stockage S3: %w", err)
	}

	return encodeRef(key), name, info.Size, nil
}

// Open récupère l'objet référencé par ref — voir port.AttachmentStore.Open.
func (s *S3Store) Open(ctx context.Context, ref string) (io.ReadCloser, string, error) {
	key, err := decodeRef(ref)
	if err != nil {
		return nil, "", err
	}

	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("lecture de la pièce jointe sur le stockage S3: %w", err)
	}
	// GetObject ne déclenche la requête réseau qu'à la première lecture —
	// Stat la force immédiatement pour remonter une éventuelle absence de
	// l'objet ici plutôt que silencieusement au premier Read de l'appelant.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, "", fmt.Errorf("lecture de la pièce jointe sur le stockage S3: %w", err)
	}

	return obj, pathpkg.Base(key), nil
}
