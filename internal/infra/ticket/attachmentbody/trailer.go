// Package attachmentbody porte le mécanisme de bookkeeping des pièces
// jointes dans le corps d'un ticket ou d'un commentaire, pour les backends
// qui n'exposent pas eux-mêmes de notion de pièce jointe distincte du corps
// (ex. GitHub), ou qui choisissent de ne pas l'utiliser (ex. Gitea, avec un
// stockage externe — cf. internal/core/port.AttachmentStore). Les
// métadonnées (id/nom/taille) sont conservées dans un bloc invisible en fin
// de corps — toujours retiré avant d'être exposé au reste d'edecán.
package attachmentbody

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"edecan/internal/core/model"
)

// requesterMetadataPattern reconnaît le bloc d'identité ajouté par
// service.TicketService (cf. appendRequesterMetadata) en fin de corps — la
// regex est dupliquée ici plutôt qu'importée : un adapter d'infrastructure
// ne doit pas dépendre de la couche service (cf. architecture hexagonale,
// internal/core/port). Cette duplication ne porte que sur un format de
// texte stable, jamais sur du code métier.
var requesterMetadataPattern = regexp.MustCompile(`(?s)\n\n---\n_Demandeur : .+? \(.+?\)_\n*$`)

// attachmentTrailerPattern délimite le bloc invisible (jamais rendu : il
// est toujours retiré avant que le corps n'atteigne le reste d'edecán) où
// sont conservées les métadonnées des pièces jointes — positionné avant le
// bloc d'identité du demandeur, qui doit lui rester en toute fin de corps
// (cf. requesterMetadataPattern, ancrée sur la fin de chaîne).
var attachmentTrailerPattern = regexp.MustCompile(`(?s)\n\n<!-- edecan:attachments\n(.*?)\n-->\n?`)

// attachmentLinePattern décrit une ligne du bloc — un champ par pièce
// jointe, nom et URL échappés (un nom de fichier peut contenir "|").
var attachmentLinePattern = regexp.MustCompile(`^edecan-attachment\|([^|]*)\|([^|]*)\|([^|]*)\|([^|]*)$`)

// Strip retire le bloc de pièces jointes du corps brut et retourne le corps
// "propre" (celui exposé au reste d'edecán) ainsi que la liste des pièces
// jointes qu'il décrivait.
func Strip(raw string) (clean string, attachments []model.Attachment) {
	loc := attachmentTrailerPattern.FindStringSubmatchIndex(raw)
	if loc == nil {
		return raw, nil
	}
	block := raw[loc[2]:loc[3]]
	clean = raw[:loc[0]] + raw[loc[1]:]
	for line := range strings.SplitSeq(block, "\n") {
		m := attachmentLinePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name, _ := url.QueryUnescape(m[2])
		size, _ := strconv.ParseInt(m[3], 10, 64)
		attachmentURL, _ := url.QueryUnescape(m[4])
		attachments = append(attachments, model.Attachment{ID: m[1], Name: name, Size: size, URL: attachmentURL})
	}
	return clean, attachments
}

// With réinsère le bloc de pièces jointes dans body, juste avant le bloc
// d'identité du demandeur s'il y en a un (sinon en toute fin) — body est
// supposé déjà dépourvu de tout bloc de pièces jointes (cf. usages,
// toujours appelés sur un corps préalablement passé par Strip).
func With(body string, attachments []model.Attachment) string {
	if len(attachments) == 0 {
		return body
	}
	var trailer strings.Builder
	trailer.WriteString("\n\n<!-- edecan:attachments\n")
	for _, a := range attachments {
		fmt.Fprintf(&trailer, "edecan-attachment|%s|%s|%d|%s\n", a.ID, url.QueryEscape(a.Name), a.Size, url.QueryEscape(a.URL))
	}
	trailer.WriteString("-->\n")

	if loc := requesterMetadataPattern.FindStringIndex(body); loc != nil {
		return body[:loc[0]] + trailer.String() + body[loc[0]:]
	}
	return body + trailer.String()
}
