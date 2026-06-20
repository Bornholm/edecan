package port

import (
	"context"
	"io"
)

// AttachmentStore abstrait le stockage du contenu binaire d'une pièce
// jointe, hors du backend de tickets — pour les backends qui n'offrent
// aucun stockage natif (ex. GitHub), ou pour ne pas faire transiter ce
// contenu par le backend de tickets pour des raisons de confidentialité
// (ex. Gitea, en alternative à son stockage natif).
type AttachmentStore interface {
	// Save persiste content sous une référence opaque, stable, et utilisable
	// comme segment d'URL (cf. route .../attachments/{id}). filename n'est
	// jamais utilisé comme chemin de confiance : storedFilename retourne le
	// nom effectivement conservé (assaini), à utiliser par l'appelant pour
	// tout affichage — garantissant qu'il correspond à celui que rendra Open.
	Save(ctx context.Context, filename string, content io.Reader) (ref string, storedFilename string, size int64, err error)

	// Open récupère le contenu et le nom de fichier d'origine précédemment
	// sauvegardés sous ref. L'appelant doit fermer le ReadCloser retourné.
	Open(ctx context.Context, ref string) (content io.ReadCloser, filename string, err error)
}
