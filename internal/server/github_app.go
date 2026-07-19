package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/iivankin/platformd/internal/githubapp"
	"github.com/iivankin/platformd/internal/state"
)

const maximumGitHubWebhookBytes = 2 << 20

func registerGitHubAppRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/settings/github", getGitHubAppSettings(config))
	mux.HandleFunc("PUT /api/v1/settings/github", putGitHubAppSettings(config))
	mux.HandleFunc("GET /api/v1/settings/github/repositories", getGitHubRepositories(config))
	mux.HandleFunc("GET /api/v1/settings/github/repositories/{repositoryID}/paths", getGitHubRepositoryPaths(config))
	mux.HandleFunc("POST /api/v1/integrations/github/webhook", handleGitHubWebhook(config))
}

func getGitHubRepositoryPaths(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		repositoryID, err := strconv.ParseInt(request.PathValue("repositoryID"), 10, 64)
		if err != nil || repositoryID <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_repository_id", "GitHub repository ID is invalid")
			return
		}
		ref := strings.TrimSpace(request.URL.Query().Get("ref"))
		query := strings.TrimSpace(request.URL.Query().Get("q"))
		kind := request.URL.Query().Get("kind")
		if ref == "" || len(ref) > 255 || len(query) > 512 || (kind != "dockerfile" && kind != "path") {
			writeAPIError(response, http.StatusBadRequest, "invalid_repository_path_query", "ref and a valid path kind are required")
			return
		}
		paths, err := config.githubApp.RepositoryPaths(request.Context(), repositoryID, ref, query, kind == "dockerfile")
		if errors.Is(err, state.ErrGitHubAppNotConfigured) {
			writeAPIError(response, http.StatusConflict, "github_app_not_configured", "Configure the GitHub App first")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusBadGateway, "github_repository_paths_failed", err.Error())
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"paths": paths})
	}
}

func publicGitHubSettings(settings githubapp.Settings) map[string]any {
	return map[string]any{
		"configured":  settings.Configured,
		"appId":       settings.AppID,
		"appSlug":     settings.AppSlug,
		"updatedAt":   settings.UpdatedAtMillis,
		"webhookPath": "/api/v1/integrations/github/webhook",
	}
}

func getGitHubAppSettings(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		settings, err := config.githubApp.Settings(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "github_app_settings_failed", "Unable to load GitHub App settings")
			return
		}
		writeJSON(response, http.StatusOK, publicGitHubSettings(settings))
	}
}

func putGitHubAppSettings(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		AppID         int64  `json:"appId"`
		PrivateKeyPEM string `json:"privateKeyPem"`
		WebhookSecret string `json:"webhookSecret"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeInstallationSettingsJSON(response, request, &body) {
			return
		}
		privateKey := []byte(body.PrivateKeyPEM)
		webhookSecret := []byte(body.WebhookSecret)
		body.PrivateKeyPEM = ""
		body.WebhookSecret = ""
		defer clear(privateKey)
		defer clear(webhookSecret)
		timestamp := config.now()
		_, auditID, requestID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "github_app_configure_failed", "Unable to allocate request IDs")
			return
		}
		settings, err := config.githubApp.Configure(request.Context(), githubapp.ConfigureInput{
			AppID: body.AppID, PrivateKeyPEM: privateKey, WebhookSecret: webhookSecret,
			AuditEventID: auditID, ActorID: identity.Subject, ActorEmail: identity.Email,
			CorrelationID: requestID, UpdatedAtMillis: timestamp.UnixMilli(),
		})
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "github_app_configure_failed", err.Error())
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, publicGitHubSettings(settings))
	}
}

func getGitHubRepositories(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		repositories, err := config.githubApp.Repositories(request.Context())
		if errors.Is(err, state.ErrGitHubAppNotConfigured) {
			writeAPIError(response, http.StatusConflict, "github_app_not_configured", "Configure the GitHub App first")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusBadGateway, "github_repositories_failed", err.Error())
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"repositories": repositories})
	}
}

func handleGitHubWebhook(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(response, request.Body, maximumGitHubWebhookBytes)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "github_webhook_invalid", "Unable to read webhook body")
			return
		}
		if err := config.githubApp.VerifyWebhook(request.Context(), body, request.Header.Get("X-Hub-Signature-256")); err != nil {
			writeAPIError(response, http.StatusUnauthorized, "github_webhook_signature", err.Error())
			return
		}
		eventName := request.Header.Get("X-GitHub-Event")
		var pushEvents []githubapp.PushEvent
		var pullRequestEvents []githubapp.PullRequestEvent
		switch eventName {
		case "push":
			event, parseErr := githubapp.ParsePush(body)
			err = parseErr
			if parseErr == nil {
				pushEvents = append(pushEvents, event)
			}
		case "pull_request":
			event, parseErr := githubapp.ParsePullRequest(body)
			err = parseErr
			if parseErr == nil {
				pullRequestEvents = append(pullRequestEvents, event)
			}
		case "check_suite", "check_run":
			push, pushErr := githubapp.ParseCheckEvent(body)
			if pushErr == nil {
				pushEvents = append(pushEvents, push)
			} else if !errors.Is(pushErr, githubapp.ErrWebhookEventIgnored) {
				err = pushErr
			}
			previews, previewErr := githubapp.ParseCheckPullRequests(body)
			if previewErr == nil {
				pullRequestEvents = append(pullRequestEvents, previews...)
			} else if !errors.Is(previewErr, githubapp.ErrWebhookEventIgnored) && err == nil {
				err = previewErr
			}
		default:
			response.WriteHeader(http.StatusAccepted)
			return
		}
		if err != nil {
			if errors.Is(err, githubapp.ErrWebhookEventIgnored) {
				response.WriteHeader(http.StatusAccepted)
				return
			}
			writeAPIError(response, http.StatusBadRequest, "github_webhook_invalid", err.Error())
			return
		}
		callbackContext := context.WithoutCancel(request.Context())
		if config.onGitHubPush != nil {
			for _, event := range pushEvents {
				go config.onGitHubPush(callbackContext, event)
			}
		}
		if config.onGitHubPullRequest != nil {
			for _, event := range pullRequestEvents {
				go config.onGitHubPullRequest(callbackContext, event)
			}
		}
		response.Header().Set("X-GitHub-Delivery", request.Header.Get("X-GitHub-Delivery"))
		response.WriteHeader(http.StatusAccepted)
	}
}
