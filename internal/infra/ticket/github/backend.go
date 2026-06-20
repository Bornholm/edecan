// Package github implémente port.TicketBackend pour GitHub (et GitHub
// Enterprise via Config.BaseURL), sur le même principe que
// internal/infra/ticket/gitea : client REST en net/http brut.
//
// Contrairement à Gitea, l'API REST de GitHub n'expose aucune notion de
// pièce jointe rattachée à une issue ou à un commentaire (pas de champ
// "assets", pas d'endpoint d'upload dédié). Les pièces jointes sont donc
// déposées dans un port.AttachmentStore externe (jamais dans le dépôt git
// lui-même — committer ce contenu dans le code source l'exposerait à un
// périmètre d'accès plus large que celui des tickets, et le laisserait dans
// l'historique git indéfiniment même après suppression), et leur métadonnée
// (id/nom/taille) est conservée dans un bloc invisible en fin de corps
// d'issue ou de commentaire (cf. internal/infra/ticket/attachmentbody) —
// toujours retiré avant d'être exposé au reste d'edecán.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"edecan/internal/core/model"
	"edecan/internal/core/port"
	"edecan/internal/infra/ticket/attachmentbody"
)

// githubAPIVersion fixe la version de l'API REST GitHub utilisée
// (https://docs.github.com/en/rest/about-the-rest-api/api-versions) — évite
// qu'un changement de version par défaut côté GitHub ne modifie
// silencieusement le comportement observé par edecán.
const githubAPIVersion = "2022-11-28"

// Backend implémente port.TicketBackend via l'API REST de GitHub, sous le
// compte technique edecán (SPEC §Glossaire : Compte technique).
type Backend struct {
	client  *http.Client
	baseURL string
	token   string // injecté via variable d'environnement, jamais en clair dans le YAML
	owner   string
	repo    string
	store   port.AttachmentStore
}

// Config regroupe les paramètres de connexion à un dépôt GitHub (ou GitHub
// Enterprise).
type Config struct {
	// BaseURL est l'URL de base de l'API REST — "https://api.github.com" par
	// défaut, ou "https://<host>/api/v3" pour une instance GitHub Enterprise.
	BaseURL string
	Token   string
	Owner   string
	Repo    string
	// AttachmentStore reçoit le contenu des pièces jointes — GitHub n'ayant
	// aucun stockage natif, ce champ est obligatoire (cf. package doc).
	AttachmentStore port.AttachmentStore
	Timeout         time.Duration // NFR : consultation de ticket < 1,5s
}

// New construit un Backend prêt à l'emploi. Timeout par défaut de 5s, BaseURL
// par défaut "https://api.github.com" si non précisés.
func New(cfg Config) *Backend {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	baseURL := strings.TrimSuffix(cfg.BaseURL, "/")
	switch baseURL {
	case "", "https://github.com":
		// Piège fréquent : "https://github.com" est l'URL web, pas l'API
		// (qui vit sur le sous-domaine "api.github.com") — on corrige plutôt
		// que de laisser toutes les requêtes échouer silencieusement contre
		// le site web (réponse HTML au lieu de JSON).
		baseURL = "https://api.github.com"
	}
	return &Backend{
		client:  &http.Client{Timeout: timeout},
		baseURL: baseURL,
		token:   cfg.Token,
		owner:   cfg.Owner,
		repo:    cfg.Repo,
		store:   cfg.AttachmentStore,
	}
}

var _ port.TicketBackend = (*Backend)(nil)

// githubIssue est la projection partielle de la réponse JSON de l'API GitHub
// pour une issue (https://docs.github.com/en/rest/issues/issues).
type githubIssue struct {
	Number    int64  `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// githubComment est la projection partielle d'un commentaire d'issue.
// Contrairement à Gitea, l'objet utilisateur retourné par l'API GitHub pour
// un commentaire ne porte que le login (pas de nom complet) : displayName
// retombe donc toujours sur le login.
type githubComment struct {
	ID        int64      `json:"id"`
	Body      string     `json:"body"`
	CreatedAt string     `json:"created_at"`
	User      githubUser `json:"user"`
}

func (c githubComment) toModel() model.Comment {
	cleanBody, attachments := attachmentbody.Strip(c.Body)
	return model.Comment{
		ID:                strconv.FormatInt(c.ID, 10),
		AuthorDisplayName: c.User.Login,
		Body:              cleanBody,
		CreatedAt:         parseGithubTime(c.CreatedAt),
		Attachments:       attachments,
	}
}

type githubUser struct {
	Login string `json:"login"`
}

func (b *Backend) issuesURL(suffix string) string {
	return fmt.Sprintf("%s/repos/%s/%s/issues%s",
		b.baseURL, url.PathEscape(b.owner), url.PathEscape(b.repo), suffix)
}

func (b *Backend) issueURL(ref model.TicketRef) string {
	return b.issuesURL("/" + url.PathEscape(string(ref)))
}

func (b *Backend) commentURL(commentID string) string {
	return fmt.Sprintf("%s/repos/%s/%s/issues/comments/%s",
		b.baseURL, url.PathEscape(b.owner), url.PathEscape(b.repo), url.PathEscape(commentID))
}

// do exécute une requête JSON authentifiée et décode la réponse dans out
// (si non nil). Retourne port.ErrTicketNotFound sur 404 (SPEC §Edge Cases :
// issue supprimée mais mapping local présent).
func (b *Backend) do(ctx context.Context, method, reqURL string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encodage de la requête github: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, reader)
	if err != nil {
		return fmt.Errorf("construction de la requête github: %w", err)
	}
	b.setCommonHeaders(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("appel de l'API github (%s %s): %w", method, reqURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return port.ErrTicketNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("réponse github %d (%s %s): %s", resp.StatusCode, method, reqURL, string(payload))
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("décodage de la réponse github: %w", err)
		}
	}
	return nil
}

func (b *Backend) setCommonHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
}

func parseGithubTime(s string) time.Time {
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

// Create crée une issue GitHub sous le compte technique
// (SPEC §Handover, point 16).
func (b *Backend) Create(ctx context.Context, t model.NewTicket) (*model.Ticket, error) {
	var issue githubIssue
	err := b.do(ctx, http.MethodPost, b.issuesURL(""), map[string]any{
		"title": t.Title,
		"body":  t.Body,
	}, &issue)
	if err != nil {
		return nil, fmt.Errorf("création de l'issue github: %w", err)
	}
	return b.toModelTicket(ctx, issue)
}

// Get recharge l'état complet de l'issue depuis GitHub, y compris ses
// commentaires — lecture à la demande (SPEC §Tickets, point 21).
func (b *Backend) Get(ctx context.Context, ref model.TicketRef) (*model.Ticket, error) {
	var issue githubIssue
	if err := b.do(ctx, http.MethodGet, b.issueURL(ref), nil, &issue); err != nil {
		return nil, err
	}
	return b.toModelTicket(ctx, issue)
}

// toModelTicket recharge les commentaires de l'issue et construit le
// model.Ticket complet, pièces jointes extraites du bloc invisible de fin de
// corps (SPEC §Tickets, point 24).
func (b *Backend) toModelTicket(ctx context.Context, issue githubIssue) (*model.Ticket, error) {
	ref := model.TicketRef(strconv.FormatInt(issue.Number, 10))

	var githubComments []githubComment
	if err := b.do(ctx, http.MethodGet, b.issueURL(ref)+"/comments", nil, &githubComments); err != nil {
		return nil, fmt.Errorf("récupération des commentaires github: %w", err)
	}

	comments := make([]model.Comment, 0, len(githubComments))
	for _, c := range githubComments {
		comments = append(comments, c.toModel())
	}

	cleanBody, attachments := attachmentbody.Strip(issue.Body)

	return &model.Ticket{
		Ref:         ref,
		Title:       issue.Title,
		Body:        cleanBody,
		Status:      toTicketStatus(issue.State),
		Comments:    comments,
		Attachments: attachments,
		CreatedAt:   parseGithubTime(issue.CreatedAt),
		UpdatedAt:   parseGithubTime(issue.UpdatedAt),
	}, nil
}

// AddComment propage un commentaire Markdown vers l'issue GitHub
// (SPEC §Tickets, point 23).
func (b *Backend) AddComment(ctx context.Context, ref model.TicketRef, c model.NewComment) (*model.Comment, error) {
	var created githubComment
	if err := b.do(ctx, http.MethodPost, b.issueURL(ref)+"/comments", map[string]any{
		"body": c.Body,
	}, &created); err != nil {
		return nil, err
	}
	m := created.toModel()
	return &m, nil
}

// SetStatus clôture ou rouvre l'issue côté GitHub. L'autorisation (User :
// clôture seule ; Support : clôture et réouverture) est vérifiée dans la
// couche service, jamais ici (cf. internal/core/service.TicketService).
func (b *Backend) SetStatus(ctx context.Context, ref model.TicketRef, status model.TicketStatus) error {
	state := "open"
	if status == model.TicketStatusClosed {
		state = "closed"
	}
	return b.do(ctx, http.MethodPatch, b.issueURL(ref), map[string]any{
		"state": state,
	}, nil)
}

// UpdateBody réécrit le corps visible de l'issue GitHub, en préservant le
// bloc de pièces jointes existant (qui n'a aucune raison d'être connu de
// l'appelant, cf. AppendAttachmentLinks côté service) — sans cette
// précaution, un appel à UpdateBody effacerait silencieusement les pièces
// jointes déjà rattachées (SPEC §Tickets, point 24).
func (b *Backend) UpdateBody(ctx context.Context, ref model.TicketRef, body string) error {
	var issue githubIssue
	if err := b.do(ctx, http.MethodGet, b.issueURL(ref), nil, &issue); err != nil {
		return err
	}
	_, attachments := attachmentbody.Strip(issue.Body)
	return b.do(ctx, http.MethodPatch, b.issueURL(ref), map[string]any{
		"body": attachmentbody.With(body, attachments),
	}, nil)
}

// UploadAttachment dépose le fichier sur le store externe et rattache sa
// métadonnée au corps de l'issue — voir port.TicketBackend.UploadAttachment
// (SPEC §Tickets, point 24).
func (b *Backend) UploadAttachment(ctx context.Context, ref model.TicketRef, filename string, content io.Reader) (*model.Attachment, error) {
	return b.appendAttachment(ctx, filename, content,
		func(ctx context.Context) (string, error) {
			var issue githubIssue
			if err := b.do(ctx, http.MethodGet, b.issueURL(ref), nil, &issue); err != nil {
				return "", err
			}
			return issue.Body, nil
		},
		func(ctx context.Context, body string) error {
			return b.do(ctx, http.MethodPatch, b.issueURL(ref), map[string]any{"body": body}, nil)
		},
	)
}

// UploadCommentAttachment dépose le fichier sur le store externe et rattache
// sa métadonnée au corps du commentaire — voir
// port.TicketBackend.UploadCommentAttachment (SPEC §Tickets, point 24).
func (b *Backend) UploadCommentAttachment(ctx context.Context, commentID, filename string, content io.Reader) (*model.Attachment, error) {
	return b.appendAttachment(ctx, filename, content,
		func(ctx context.Context) (string, error) {
			var c githubComment
			if err := b.do(ctx, http.MethodGet, b.commentURL(commentID), nil, &c); err != nil {
				return "", err
			}
			return c.Body, nil
		},
		func(ctx context.Context, body string) error {
			return b.do(ctx, http.MethodPatch, b.commentURL(commentID), map[string]any{"body": body}, nil)
		},
	)
}

// appendAttachment dépose le fichier sur le store externe puis
// recharge/réécrit le corps porté par load/save pour y ajouter la métadonnée
// de la nouvelle pièce jointe au bloc existant — partagé entre pièces
// jointes de ticket et de commentaire, seul le corps cible différant
// (SPEC §Tickets, point 24).
func (b *Backend) appendAttachment(
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
		return nil, fmt.Errorf("lecture du corps avant rattachement de la pièce jointe github: %w", err)
	}
	cleanBody, attachments := attachmentbody.Strip(raw)
	attachments = append(attachments, *attachment)
	if err := save(ctx, attachmentbody.With(cleanBody, attachments)); err != nil {
		return nil, fmt.Errorf("enregistrement de la pièce jointe github: %w", err)
	}

	return attachment, nil
}

// DownloadAttachment récupère le contenu d'une pièce jointe depuis le store
// externe (SPEC §Tickets, point 24).
func (b *Backend) DownloadAttachment(ctx context.Context, ref model.TicketRef, attachmentID string) (io.ReadCloser, string, error) {
	return b.store.Open(ctx, attachmentID)
}
