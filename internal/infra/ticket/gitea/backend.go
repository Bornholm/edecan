// Package gitea implémente port.TicketBackend pour Gitea, le backend de
// tickets par défaut (SPEC §Glossaire). Implémenté en net/http brut plutôt
// que via le SDK officiel (code.gitea.io/sdk/gitea) afin d'éviter d'imposer
// une bascule de toolchain Go et un graphe de dépendances disproportionné
// pour un client REST somme toute simple.
package gitea

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
	"edecan/internal/infra/ticket/attachmentbody"
)

// Backend implémente port.TicketBackend via l'API REST de Gitea, sous le
// compte technique edecán (SPEC §Glossaire : Compte technique).
type Backend struct {
	client  *http.Client
	baseURL string
	token   string // injecté via variable d'environnement, jamais en clair dans le YAML
	owner   string
	repo    string
	// store, si non nil, dévie les pièces jointes vers un stockage externe
	// au lieu du stockage natif Gitea (cf. Config.AttachmentStore).
	store port.AttachmentStore
}

// Config regroupe les paramètres de connexion à une instance Gitea.
type Config struct {
	BaseURL string
	Token   string
	Owner   string
	Repo    string
	// AttachmentStore est optionnel : laissé nil, les pièces jointes
	// utilisent le stockage natif de Gitea (assets) ; renseigné, elles sont
	// déposées sur ce store à la place (cf. package doc github, même
	// principe, par souci de confidentialité — les pièces jointes
	// existantes uploadées nativement avant ce changement restent
	// accessibles, cf. DownloadAttachment).
	AttachmentStore port.AttachmentStore
	Timeout         time.Duration // NFR : consultation de ticket < 1,5s
}

// New construit un Backend prêt à l'emploi. Timeout par défaut de 5s si non précisé.
func New(cfg Config) *Backend {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &Backend{
		client:  &http.Client{Timeout: timeout},
		baseURL: cfg.BaseURL,
		token:   cfg.Token,
		owner:   cfg.Owner,
		repo:    cfg.Repo,
		store:   cfg.AttachmentStore,
	}
}

var _ port.TicketBackend = (*Backend)(nil)

// giteaIssue est la projection partielle de la réponse JSON de l'API Gitea
// pour une issue (https://gitea.io/api/swagger, schéma Issue).
type giteaIssue struct {
	Index     int64             `json:"number"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	State     string            `json:"state"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at"`
	Assets    []giteaAttachment `json:"assets"`
}

// giteaComment inclut directement ses pièces jointes (champ "assets") : un
// agent support répondant depuis l'interface native de Gitea (sans passer
// par edecán) y attache ses fichiers au niveau du commentaire, pas de
// l'issue — elles n'apparaîtraient jamais dans /issues/{ref}/assets
// (SPEC §Tickets, point 24 ; constaté sur forge.cadoles.com/kipp/edecan-test/issues/3).
type giteaComment struct {
	ID        int64             `json:"id"`
	Body      string            `json:"body"`
	CreatedAt string            `json:"created_at"`
	User      giteaUser         `json:"user"`
	Assets    []giteaAttachment `json:"assets"`
}

func (c giteaComment) toModel() *model.Comment {
	// Strip retire un éventuel bloc de pièces jointes externes (mode
	// Config.AttachmentStore) — sans effet si absent (mode natif Gitea).
	cleanBody, externalAttachments := attachmentbody.Strip(c.Body)
	return &model.Comment{
		ID:                strconv.FormatInt(c.ID, 10),
		AuthorDisplayName: displayName(c.User),
		Body:              cleanBody,
		CreatedAt:         parseGiteaTime(c.CreatedAt),
		Attachments:       append(toModelAttachments(c.Assets), externalAttachments...),
	}
}

type giteaUser struct {
	FullName string `json:"full_name"`
	UserName string `json:"login"`
}

type giteaAttachment struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"browser_download_url"`
}

func (b *Backend) issuesURL(suffix string) string {
	return fmt.Sprintf("%s/api/v1/repos/%s/%s/issues%s",
		b.baseURL, url.PathEscape(b.owner), url.PathEscape(b.repo), suffix)
}

// commentAssetsURL construit l'URL des pièces jointes d'un commentaire
// (endpoint Gitea distinct de celui des pièces jointes du ticket).
func (b *Backend) commentAssetsURL(commentID, suffix string) string {
	return fmt.Sprintf("%s/api/v1/repos/%s/%s/issues/comments/%s/assets%s",
		b.baseURL, url.PathEscape(b.owner), url.PathEscape(b.repo), url.PathEscape(commentID), suffix)
}

// commentURL construit l'URL d'un commentaire (lecture/écriture de son
// corps) — utilisé uniquement en mode stockage externe (cf. Config.AttachmentStore).
func (b *Backend) commentURL(commentID string) string {
	return fmt.Sprintf("%s/api/v1/repos/%s/%s/issues/comments/%s",
		b.baseURL, url.PathEscape(b.owner), url.PathEscape(b.repo), url.PathEscape(commentID))
}

// isNumeric signale si s ne contient que des chiffres — utilisé pour
// distinguer un identifiant d'asset natif Gitea (entier décimal) d'une
// référence opaque de stockage externe (base64, jamais entièrement
// numérique en pratique — cf. DownloadAttachment).
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// do exécute une requête JSON authentifiée et décode la réponse dans out
// (si non nil). Retourne port.ErrTicketNotFound sur 404 (SPEC §Edge Cases :
// issue supprimée mais mapping local présent).
func (b *Backend) do(ctx context.Context, method, reqURL string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encodage de la requête gitea: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, reader)
	if err != nil {
		return fmt.Errorf("construction de la requête gitea: %w", err)
	}
	req.Header.Set("Authorization", "token "+b.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("appel de l'API gitea (%s %s): %w", method, reqURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return port.ErrTicketNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("réponse gitea %d (%s %s): %s", resp.StatusCode, method, reqURL, string(payload))
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("décodage de la réponse gitea: %w", err)
		}
	}
	return nil
}

func parseGiteaTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func toTicketStatus(state string) model.TicketStatus {
	if state == "closed" {
		return model.TicketStatusClosed
	}
	return model.TicketStatusOpen
}

func displayName(u giteaUser) string {
	if u.FullName != "" {
		return u.FullName
	}
	return u.UserName
}

// Create crée une issue Gitea sous le compte technique
// (SPEC §Handover, point 16).
func (b *Backend) Create(ctx context.Context, t model.NewTicket) (*model.Ticket, error) {
	var issue giteaIssue
	err := b.do(ctx, http.MethodPost, b.issuesURL(""), map[string]any{
		"title": t.Title,
		"body":  t.Body,
	}, &issue)
	if err != nil {
		return nil, fmt.Errorf("création de l'issue gitea: %w", err)
	}
	return b.toModelTicket(ctx, issue)
}

// Get recharge l'état complet de l'issue depuis Gitea, y compris ses
// commentaires — lecture à la demande (SPEC §Tickets, point 21).
func (b *Backend) Get(ctx context.Context, ref model.TicketRef) (*model.Ticket, error) {
	var issue giteaIssue
	if err := b.do(ctx, http.MethodGet, b.issuesURL("/"+string(ref)), nil, &issue); err != nil {
		return nil, err
	}
	return b.toModelTicket(ctx, issue)
}

// toModelTicket recharge les commentaires et les pièces jointes de l'issue et
// construit le model.Ticket complet (SPEC §Tickets, point 24).
func (b *Backend) toModelTicket(ctx context.Context, issue giteaIssue) (*model.Ticket, error) {
	ref := model.TicketRef(strconv.FormatInt(issue.Index, 10))

	var giteaComments []giteaComment
	if err := b.do(ctx, http.MethodGet, b.issuesURL(fmt.Sprintf("/%s/comments", ref)), nil, &giteaComments); err != nil {
		return nil, fmt.Errorf("récupération des commentaires gitea: %w", err)
	}

	comments := make([]model.Comment, 0, len(giteaComments))
	for _, c := range giteaComments {
		comments = append(comments, *c.toModel())
	}

	// Strip retire un éventuel bloc de pièces jointes externes (mode
	// Config.AttachmentStore) — sans effet si absent (mode natif Gitea).
	cleanBody, externalAttachments := attachmentbody.Strip(issue.Body)

	return &model.Ticket{
		Ref:         ref,
		Title:       issue.Title,
		Body:        cleanBody,
		Status:      toTicketStatus(issue.State),
		Comments:    comments,
		Attachments: append(toModelAttachments(issue.Assets), externalAttachments...),
		CreatedAt:   parseGiteaTime(issue.CreatedAt),
		UpdatedAt:   parseGiteaTime(issue.UpdatedAt),
	}, nil
}

func toModelAttachments(assets []giteaAttachment) []model.Attachment {
	attachments := make([]model.Attachment, 0, len(assets))
	for _, a := range assets {
		attachments = append(attachments, model.Attachment{
			ID:   strconv.FormatInt(a.ID, 10),
			Name: a.Name,
			URL:  a.DownloadURL,
			Size: a.Size,
		})
	}
	return attachments
}

// AddComment propage un commentaire Markdown vers l'issue Gitea
// (SPEC §Tickets, point 23).
func (b *Backend) AddComment(ctx context.Context, ref model.TicketRef, c model.NewComment) (*model.Comment, error) {
	var created giteaComment
	if err := b.do(ctx, http.MethodPost, b.issuesURL("/"+string(ref)+"/comments"), map[string]any{
		"body": c.Body,
	}, &created); err != nil {
		return nil, err
	}
	return created.toModel(), nil
}

// SetStatus clôture ou rouvre l'issue côté Gitea. L'autorisation (User :
// clôture seule ; Support : clôture et réouverture) est vérifiée dans la
// couche service, jamais ici (cf. internal/core/service.TicketService).
func (b *Backend) SetStatus(ctx context.Context, ref model.TicketRef, status model.TicketStatus) error {
	state := "open"
	if status == model.TicketStatusClosed {
		state = "closed"
	}
	return b.do(ctx, http.MethodPatch, b.issuesURL("/"+string(ref)), map[string]any{
		"state": state,
	}, nil)
}

// UpdateBody réécrit le corps de l'issue Gitea (SPEC §Tickets, point 24). En
// mode stockage externe (Config.AttachmentStore non nil), préserve le bloc
// de pièces jointes existant — sans cette précaution, cet appel l'effacerait
// silencieusement (même raison que le backend github, cf. son UpdateBody).
func (b *Backend) UpdateBody(ctx context.Context, ref model.TicketRef, body string) error {
	if b.store == nil {
		return b.do(ctx, http.MethodPatch, b.issuesURL("/"+string(ref)), map[string]any{
			"body": body,
		}, nil)
	}
	var issue giteaIssue
	if err := b.do(ctx, http.MethodGet, b.issuesURL("/"+string(ref)), nil, &issue); err != nil {
		return err
	}
	_, attachments := attachmentbody.Strip(issue.Body)
	return b.do(ctx, http.MethodPatch, b.issuesURL("/"+string(ref)), map[string]any{
		"body": attachmentbody.With(body, attachments),
	}, nil)
}

// UploadAttachment transmet un fichier, rattaché au ticket — voir
// port.TicketBackend.UploadAttachment (SPEC §Tickets, point 24). Stockage
// natif Gitea par défaut, ou store externe si Config.AttachmentStore est
// renseigné (cf. package doc).
func (b *Backend) UploadAttachment(ctx context.Context, ref model.TicketRef, filename string, content io.Reader) (*model.Attachment, error) {
	if b.store == nil {
		return b.uploadAsset(ctx, b.issuesURL("/"+string(ref)+"/assets"), filename, content)
	}
	issueURL := b.issuesURL("/" + string(ref))
	return b.appendExternalAttachment(ctx, filename, content,
		func(ctx context.Context) (string, error) {
			var issue giteaIssue
			if err := b.do(ctx, http.MethodGet, issueURL, nil, &issue); err != nil {
				return "", err
			}
			return issue.Body, nil
		},
		func(ctx context.Context, body string) error {
			return b.do(ctx, http.MethodPatch, issueURL, map[string]any{"body": body}, nil)
		},
	)
}

// UploadCommentAttachment transmet un fichier, rattaché à un commentaire
// précis — voir port.TicketBackend.UploadCommentAttachment (SPEC §Tickets,
// point 24). Stockage natif Gitea par défaut ("issue comment assets"), ou
// store externe si Config.AttachmentStore est renseigné.
func (b *Backend) UploadCommentAttachment(ctx context.Context, commentID, filename string, content io.Reader) (*model.Attachment, error) {
	if b.store == nil {
		return b.uploadAsset(ctx, b.commentAssetsURL(commentID, ""), filename, content)
	}
	commentURL := b.commentURL(commentID)
	return b.appendExternalAttachment(ctx, filename, content,
		func(ctx context.Context) (string, error) {
			var c giteaComment
			if err := b.do(ctx, http.MethodGet, commentURL, nil, &c); err != nil {
				return "", err
			}
			return c.Body, nil
		},
		func(ctx context.Context, body string) error {
			return b.do(ctx, http.MethodPatch, commentURL, map[string]any{"body": body}, nil)
		},
	)
}

// appendExternalAttachment dépose le fichier sur le store externe puis
// recharge/réécrit le corps porté par load/save pour y ajouter la métadonnée
// de la nouvelle pièce jointe au bloc existant — même principe que le
// backend github (cf. son appendAttachment).
func (b *Backend) appendExternalAttachment(
	ctx context.Context,
	filename string,
	content io.Reader,
	load func(ctx context.Context) (string, error),
	save func(ctx context.Context, body string) error,
) (*model.Attachment, error) {
	ref, storedFilename, size, err := b.store.Save(ctx, filename, content)
	if err != nil {
		return nil, fmt.Errorf("dépôt de la pièce jointe: %w", err)
	}
	attachment := &model.Attachment{ID: ref, Name: storedFilename, Size: size}

	raw, err := load(ctx)
	if err != nil {
		return nil, fmt.Errorf("lecture du corps avant rattachement de la pièce jointe gitea: %w", err)
	}
	cleanBody, attachments := attachmentbody.Strip(raw)
	attachments = append(attachments, *attachment)
	if err := save(ctx, attachmentbody.With(cleanBody, attachments)); err != nil {
		return nil, fmt.Errorf("enregistrement de la pièce jointe gitea: %w", err)
	}

	return attachment, nil
}

// uploadAsset envoie le multipart d'upload vers reqURL — partagé entre les
// assets de ticket et de commentaire, Gitea exposant le même format de
// requête et de réponse pour les deux (SPEC §Tickets, point 24).
func (b *Backend) uploadAsset(ctx context.Context, reqURL, filename string, content io.Reader) (*model.Attachment, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("attachment", filename)
	if err != nil {
		return nil, fmt.Errorf("préparation de l'upload gitea: %w", err)
	}
	if _, err := io.Copy(part, content); err != nil {
		return nil, fmt.Errorf("lecture du fichier à téléverser: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finalisation de l'upload gitea: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("construction de la requête d'upload gitea: %w", err)
	}
	req.Header.Set("Authorization", "token "+b.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("appel de l'upload gitea: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, port.ErrTicketNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("réponse gitea %d lors de l'upload: %s", resp.StatusCode, string(payload))
	}

	var asset giteaAttachment
	if err := json.NewDecoder(resp.Body).Decode(&asset); err != nil {
		return nil, fmt.Errorf("décodage de la réponse d'upload gitea: %w", err)
	}

	return &model.Attachment{ID: strconv.FormatInt(asset.ID, 10), Name: asset.Name, URL: asset.DownloadURL, Size: asset.Size}, nil
}

// DownloadAttachment récupère le contenu d'une pièce jointe — depuis Gitea
// si attachmentID est un identifiant d'asset natif (entier décimal), depuis
// le store externe sinon (référence opaque, cf. Config.AttachmentStore) ;
// permet la cohabitation des pièces jointes historiques (uploadées
// nativement avant l'activation du store externe) avec les nouvelles, sans
// migration (SPEC §Tickets, point 24).
func (b *Backend) DownloadAttachment(ctx context.Context, ref model.TicketRef, attachmentID string) (io.ReadCloser, string, error) {
	if b.store != nil && !isNumeric(attachmentID) {
		return b.store.Open(ctx, attachmentID)
	}

	var asset giteaAttachment
	if err := b.do(ctx, http.MethodGet, b.issuesURL("/"+string(ref)+"/assets/"+attachmentID), nil, &asset); err != nil {
		return nil, "", fmt.Errorf("lecture des métadonnées de la pièce jointe gitea: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.DownloadURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("construction de la requête de téléchargement gitea: %w", err)
	}
	// Authorization n'est transmise par net/http que vers le même hôte lors
	// d'une éventuelle redirection — comportement de sécurité par défaut du
	// client Go, adapté ici puisque asset.DownloadURL peut tout aussi bien
	// pointer vers un stockage externe pré-signé que vers Gitea lui-même.
	req.Header.Set("Authorization", "token "+b.token)

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("téléchargement de la pièce jointe gitea: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, "", port.ErrTicketNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, "", fmt.Errorf("réponse gitea %d lors du téléchargement: %s", resp.StatusCode, string(payload))
	}

	return resp.Body, asset.Name, nil
}
