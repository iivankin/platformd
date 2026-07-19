package githubapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

type Repository interface {
	GitHubAppSettings(context.Context) (state.GitHubAppSettings, error)
	PutGitHubAppSettings(context.Context, state.PutGitHubAppSettingsInput) error
}

type Config struct {
	Repository     Repository
	Master         cryptobox.MasterKey
	InstallationID string
	HTTPClient     *http.Client
	BaseURL        string
	Now            func() time.Time
}

type Application struct {
	repository     Repository
	master         cryptobox.MasterKey
	installationID string
	client         *http.Client
	baseURL        string
	now            func() time.Time
}

type Settings struct {
	Configured      bool
	AppID           int64
	AppSlug         string
	UpdatedAtMillis int64
}

type ConfigureInput struct {
	AppID           int64
	PrivateKeyPEM   []byte
	WebhookSecret   []byte
	AuditEventID    string
	ActorID         string
	ActorEmail      string
	CorrelationID   string
	UpdatedAtMillis int64
}

func New(config Config) (*Application, error) {
	if config.Repository == nil || config.InstallationID == "" {
		return nil, errors.New("GitHub App dependencies are incomplete")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Application{
		repository: config.Repository, master: config.Master,
		installationID: config.InstallationID, client: client, baseURL: baseURL, now: now,
	}, nil
}

func (application *Application) Settings(ctx context.Context) (Settings, error) {
	stored, err := application.repository.GitHubAppSettings(ctx)
	if errors.Is(err, state.ErrGitHubAppNotConfigured) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	return Settings{Configured: true, AppID: stored.AppID, AppSlug: stored.AppSlug, UpdatedAtMillis: stored.UpdatedAtMillis}, nil
}

func (application *Application) Configure(ctx context.Context, input ConfigureInput) (Settings, error) {
	if input.AppID <= 0 || len(input.PrivateKeyPEM) == 0 || len(input.WebhookSecret) < 16 ||
		input.AuditEventID == "" || input.ActorID == "" || input.ActorEmail == "" || input.UpdatedAtMillis <= 0 {
		return Settings{}, errors.New("GitHub App configuration is incomplete; webhook secret must contain at least 16 bytes")
	}
	key, err := parsePrivateKey(input.PrivateKeyPEM)
	if err != nil {
		return Settings{}, err
	}
	token, err := appJWT(input.AppID, key, application.now())
	if err != nil {
		return Settings{}, err
	}
	var app struct {
		Slug string `json:"slug"`
	}
	if err := application.request(ctx, http.MethodGet, "/app", token, nil, &app); err != nil {
		return Settings{}, err
	}
	if app.Slug == "" {
		return Settings{}, errors.New("GitHub App response has no slug")
	}
	box, err := newBox(application.master, application.installationID)
	if err != nil {
		return Settings{}, err
	}
	sealedKey, err := seal(box, "private-key", input.PrivateKeyPEM)
	if err != nil {
		return Settings{}, err
	}
	sealedSecret, err := seal(box, "webhook-secret", input.WebhookSecret)
	if err != nil {
		return Settings{}, err
	}
	if err := application.repository.PutGitHubAppSettings(ctx, state.PutGitHubAppSettingsInput{
		Settings: state.GitHubAppSettings{
			AppID: input.AppID, AppSlug: app.Slug, PrivateKeyEncrypted: sealedKey,
			WebhookSecretEncrypted: sealedSecret,
		},
		AuditEventID: input.AuditEventID, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
		CorrelationID: input.CorrelationID, UpdatedAtMillis: input.UpdatedAtMillis,
	}); err != nil {
		return Settings{}, err
	}
	return application.Settings(ctx)
}

func (application *Application) openCredentials(ctx context.Context) (credentials, []byte, error) {
	stored, err := application.repository.GitHubAppSettings(ctx)
	if err != nil {
		return credentials{}, nil, err
	}
	box, err := newBox(application.master, application.installationID)
	if err != nil {
		return credentials{}, nil, err
	}
	privateKey, err := open(box, "private-key", stored.PrivateKeyEncrypted)
	if err != nil {
		return credentials{}, nil, err
	}
	secret, err := open(box, "webhook-secret", stored.WebhookSecretEncrypted)
	if err != nil {
		clear(privateKey)
		return credentials{}, nil, err
	}
	return credentials{appID: stored.AppID, privateKey: privateKey}, secret, nil
}

func (application *Application) VerifyWebhook(ctx context.Context, body []byte, signature string) error {
	value, secret, err := application.openCredentials(ctx)
	if err != nil {
		return err
	}
	clear(value.privateKey)
	defer clear(secret)
	if !strings.HasPrefix(signature, "sha256=") {
		return errors.New("GitHub webhook signature is missing")
	}
	received, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return errors.New("GitHub webhook signature is invalid")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	if !hmac.Equal(received, mac.Sum(nil)) {
		return errors.New("GitHub webhook signature does not match")
	}
	return nil
}

type PushEvent struct {
	RepositoryID int64
	Branch       string
	Revision     string
	ChangedPaths []string
	ChecksEvent  bool
}

type PullRequestEvent struct {
	Action       string
	RepositoryID int64
	Number       int
	BaseBranch   string
	Revision     string
	ChecksEvent  bool
}

var ErrWebhookEventIgnored = errors.New("GitHub webhook event does not require deployment")

func ParsePush(body []byte) (PushEvent, error) {
	var payload struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Repository struct {
			ID int64 `json:"id"`
		} `json:"repository"`
		Commits []struct {
			Added    []string `json:"added"`
			Modified []string `json:"modified"`
			Removed  []string `json:"removed"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return PushEvent{}, err
	}
	if payload.Repository.ID <= 0 || !strings.HasPrefix(payload.Ref, "refs/heads/") {
		return PushEvent{}, errors.New("GitHub push payload is incomplete")
	}
	if payload.After == strings.Repeat("0", 40) {
		return PushEvent{}, ErrWebhookEventIgnored
	}
	if !validCommitSHA(payload.After) {
		return PushEvent{}, errors.New("GitHub push payload has an invalid commit SHA")
	}
	seen := make(map[string]struct{})
	paths := make([]string, 0)
	for _, commit := range payload.Commits {
		for _, path := range append(append(commit.Added, commit.Modified...), commit.Removed...) {
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	return PushEvent{
		RepositoryID: payload.Repository.ID,
		Branch:       strings.TrimPrefix(payload.Ref, "refs/heads/"), Revision: payload.After, ChangedPaths: paths,
	}, nil
}

func ParseCheckEvent(body []byte) (PushEvent, error) {
	var payload struct {
		Action     string `json:"action"`
		Repository struct {
			ID int64 `json:"id"`
		} `json:"repository"`
		CheckSuite struct {
			HeadBranch string `json:"head_branch"`
			HeadSHA    string `json:"head_sha"`
		} `json:"check_suite"`
		CheckRun struct {
			HeadSHA    string `json:"head_sha"`
			CheckSuite struct {
				HeadBranch string `json:"head_branch"`
				HeadSHA    string `json:"head_sha"`
			} `json:"check_suite"`
		} `json:"check_run"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return PushEvent{}, err
	}
	if payload.Action != "completed" {
		return PushEvent{}, ErrWebhookEventIgnored
	}
	branch := payload.CheckSuite.HeadBranch
	if branch == "" {
		branch = payload.CheckRun.CheckSuite.HeadBranch
	}
	revision := payload.CheckSuite.HeadSHA
	if revision == "" {
		revision = payload.CheckRun.HeadSHA
	}
	if revision == "" {
		revision = payload.CheckRun.CheckSuite.HeadSHA
	}
	if payload.Repository.ID <= 0 || branch == "" || !validCommitSHA(revision) {
		return PushEvent{}, errors.New("GitHub check payload is incomplete")
	}
	return PushEvent{
		RepositoryID: payload.Repository.ID, Branch: branch, Revision: revision,
		ChecksEvent: true,
	}, nil
}

func ParsePullRequest(body []byte) (PullRequestEvent, error) {
	var payload struct {
		Action     string `json:"action"`
		Number     int    `json:"number"`
		Repository struct {
			ID int64 `json:"id"`
		} `json:"repository"`
		PullRequest struct {
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return PullRequestEvent{}, err
	}
	switch payload.Action {
	case "opened", "reopened", "synchronize", "closed":
	default:
		return PullRequestEvent{}, ErrWebhookEventIgnored
	}
	if payload.Repository.ID <= 0 || payload.Number <= 0 || payload.PullRequest.Base.Ref == "" ||
		!validCommitSHA(payload.PullRequest.Head.SHA) {
		return PullRequestEvent{}, errors.New("GitHub pull request payload is incomplete")
	}
	action := "deploy"
	if payload.Action == "closed" {
		action = "close"
	}
	return PullRequestEvent{
		Action: action, RepositoryID: payload.Repository.ID, Number: payload.Number,
		BaseBranch: payload.PullRequest.Base.Ref, Revision: payload.PullRequest.Head.SHA,
	}, nil
}

func ParseCheckPullRequests(body []byte) ([]PullRequestEvent, error) {
	var payload struct {
		Action     string `json:"action"`
		Repository struct {
			ID int64 `json:"id"`
		} `json:"repository"`
		CheckSuite struct {
			HeadSHA      string `json:"head_sha"`
			PullRequests []struct {
				Number int `json:"number"`
				Base   struct {
					Ref string `json:"ref"`
				} `json:"base"`
				Head struct {
					SHA string `json:"sha"`
				} `json:"head"`
			} `json:"pull_requests"`
		} `json:"check_suite"`
		CheckRun struct {
			HeadSHA      string `json:"head_sha"`
			PullRequests []struct {
				Number int `json:"number"`
				Base   struct {
					Ref string `json:"ref"`
				} `json:"base"`
				Head struct {
					SHA string `json:"sha"`
				} `json:"head"`
			} `json:"pull_requests"`
		} `json:"check_run"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload.Action != "completed" {
		return nil, ErrWebhookEventIgnored
	}
	if payload.Repository.ID <= 0 {
		return nil, errors.New("GitHub check payload has no repository")
	}
	pullRequests := payload.CheckSuite.PullRequests
	revision := payload.CheckSuite.HeadSHA
	if len(pullRequests) == 0 {
		pullRequests = payload.CheckRun.PullRequests
		revision = payload.CheckRun.HeadSHA
	}
	result := make([]PullRequestEvent, 0, len(pullRequests))
	for _, pullRequest := range pullRequests {
		sha := pullRequest.Head.SHA
		if sha == "" {
			sha = revision
		}
		if pullRequest.Number <= 0 || pullRequest.Base.Ref == "" || !validCommitSHA(sha) {
			continue
		}
		result = append(result, PullRequestEvent{
			Action: "deploy", RepositoryID: payload.Repository.ID, Number: pullRequest.Number,
			BaseBranch: pullRequest.Base.Ref, Revision: sha, ChecksEvent: true,
		})
	}
	if len(result) == 0 {
		return nil, ErrWebhookEventIgnored
	}
	return result, nil
}

func validCommitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	return strings.IndexFunc(value, func(character rune) bool {
		return (character < '0' || character > '9') && (character < 'a' || character > 'f')
	}) == -1
}
