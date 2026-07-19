package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	maximumArchiveBytes              = 512 << 20
	maximumRepositoryPathSuggestions = 200
)

type RepositoryInfo struct {
	ID             int64  `json:"id"`
	FullName       string `json:"fullName"`
	DefaultBranch  string `json:"defaultBranch"`
	InstallationID int64  `json:"installationId"`
}

type RepositoryPath struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

type Commit struct {
	SHA          string
	Message      string
	ChangedPaths []string
}

type CheckState string

const (
	ChecksPending CheckState = "pending"
	ChecksPassed  CheckState = "passed"
	ChecksFailed  CheckState = "failed"
)

type Deployment struct {
	ID int64
}

type CreateDeploymentInput struct {
	RepositoryID          int64
	Ref                   string
	Environment           string
	Description           string
	PlatformdDeploymentID string
	TransientEnvironment  bool
	ProductionEnvironment bool
}

type DeploymentStatusState string

const (
	DeploymentInProgress DeploymentStatusState = "in_progress"
	DeploymentSuccess    DeploymentStatusState = "success"
	DeploymentFailure    DeploymentStatusState = "failure"
	DeploymentInactive   DeploymentStatusState = "inactive"
)

type CreateDeploymentStatusInput struct {
	RepositoryID   int64
	DeploymentID   int64
	State          DeploymentStatusState
	Environment    string
	Description    string
	LogURL         string
	EnvironmentURL string
}

type IssueComment struct {
	ID int64
}

type credentials struct {
	appID      int64
	privateKey []byte
}

func (application *Application) request(ctx context.Context, method, path, token string, body any, destination any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, application.baseURL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "platformd")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := application.client.Do(request)
	if err != nil {
		return fmt.Errorf("GitHub API request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("GitHub API %s %s returned %d: %s", method, path, response.StatusCode, strings.TrimSpace(string(message)))
	}
	if destination == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(destination); err != nil {
		return fmt.Errorf("decode GitHub API response: %w", err)
	}
	return nil
}

func (application *Application) appToken(value credentials) (string, error) {
	key, err := parsePrivateKey(value.privateKey)
	if err != nil {
		return "", err
	}
	return appJWT(value.appID, key, application.now())
}

func (application *Application) installations(ctx context.Context, value credentials) ([]int64, error) {
	token, err := application.appToken(value)
	if err != nil {
		return nil, err
	}
	var payload []struct {
		ID int64 `json:"id"`
	}
	if err := application.request(ctx, http.MethodGet, "/app/installations?per_page=100", token, nil, &payload); err != nil {
		return nil, err
	}
	result := make([]int64, 0, len(payload))
	for _, installation := range payload {
		result = append(result, installation.ID)
	}
	return result, nil
}

func (application *Application) installationToken(ctx context.Context, value credentials, installationID int64) (string, error) {
	appToken, err := application.appToken(value)
	if err != nil {
		return "", err
	}
	var payload struct {
		Token string `json:"token"`
	}
	path := "/app/installations/" + strconv.FormatInt(installationID, 10) + "/access_tokens"
	if err := application.request(ctx, http.MethodPost, path, appToken, map[string]any{}, &payload); err != nil {
		return "", err
	}
	if payload.Token == "" {
		return "", errors.New("GitHub returned an empty installation token")
	}
	return payload.Token, nil
}

func (application *Application) Repositories(ctx context.Context) ([]RepositoryInfo, error) {
	value, _, err := application.openCredentials(ctx)
	if err != nil {
		return nil, err
	}
	defer clear(value.privateKey)
	installations, err := application.installations(ctx, value)
	if err != nil {
		return nil, err
	}
	result := make([]RepositoryInfo, 0)
	for _, installationID := range installations {
		token, err := application.installationToken(ctx, value, installationID)
		if err != nil {
			return nil, err
		}
		var payload struct {
			Repositories []struct {
				ID            int64  `json:"id"`
				FullName      string `json:"full_name"`
				DefaultBranch string `json:"default_branch"`
			} `json:"repositories"`
		}
		if err := application.request(ctx, http.MethodGet, "/installation/repositories?per_page=100", token, nil, &payload); err != nil {
			return nil, err
		}
		for _, repository := range payload.Repositories {
			result = append(result, RepositoryInfo{
				ID: repository.ID, FullName: repository.FullName,
				DefaultBranch: repository.DefaultBranch, InstallationID: installationID,
			})
		}
	}
	return result, nil
}

func (application *Application) repositoryToken(ctx context.Context, repositoryID int64) (RepositoryInfo, string, error) {
	repositories, err := application.Repositories(ctx)
	if err != nil {
		return RepositoryInfo{}, "", err
	}
	for _, repository := range repositories {
		if repository.ID != repositoryID {
			continue
		}
		value, _, err := application.openCredentials(ctx)
		if err != nil {
			return RepositoryInfo{}, "", err
		}
		defer clear(value.privateKey)
		token, err := application.installationToken(ctx, value, repository.InstallationID)
		return repository, token, err
	}
	return RepositoryInfo{}, "", errors.New("GitHub repository is not installed for this App")
}

func (application *Application) RepositoryPaths(
	ctx context.Context,
	repositoryID int64,
	ref string,
	query string,
	dockerfilesOnly bool,
) ([]RepositoryPath, error) {
	repository, token, err := application.repositoryToken(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("GitHub tree ref is empty")
	}
	var payload struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	path := "/repos/" + repository.FullName + "/git/trees/" + url.PathEscape(ref) + "?recursive=1"
	if err := application.request(ctx, http.MethodGet, path, token, nil, &payload); err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	result := make([]RepositoryPath, 0, maximumRepositoryPathSuggestions)
	for _, item := range payload.Tree {
		candidate := strings.TrimSpace(item.Path)
		if candidate == "" || (item.Type != "blob" && item.Type != "tree") {
			continue
		}
		if dockerfilesOnly && (item.Type != "blob" || !strings.Contains(strings.ToLower(candidate), "dockerfile")) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(candidate), needle) {
			continue
		}
		result = append(result, RepositoryPath{Path: candidate, Type: item.Type})
		if len(result) == maximumRepositoryPathSuggestions {
			break
		}
	}
	return result, nil
}

func (application *Application) Commit(ctx context.Context, repositoryID int64, revision string) (Commit, error) {
	repository, token, err := application.repositoryToken(ctx, repositoryID)
	if err != nil {
		return Commit{}, err
	}
	var payload struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
		Files []struct {
			Filename string `json:"filename"`
		} `json:"files"`
	}
	basePath := "/repos/" + repository.FullName + "/commits/" + url.PathEscape(revision)
	paths := make([]string, 0)
	for page := 1; page <= 30; page++ {
		payload.Files = nil
		path := basePath + "?per_page=100&page=" + strconv.Itoa(page)
		if err := application.request(ctx, http.MethodGet, path, token, nil, &payload); err != nil {
			return Commit{}, err
		}
		for _, file := range payload.Files {
			paths = append(paths, file.Filename)
		}
		if len(payload.Files) < 100 {
			break
		}
	}
	if payload.SHA == "" {
		return Commit{}, errors.New("GitHub commit response has no SHA")
	}
	return Commit{
		SHA: payload.SHA, Message: strings.Split(payload.Commit.Message, "\n")[0],
		ChangedPaths: paths,
	}, nil
}

func (application *Application) Checks(ctx context.Context, repositoryID int64, sha string) (CheckState, error) {
	repository, token, err := application.repositoryToken(ctx, repositoryID)
	if err != nil {
		return "", err
	}
	var checks struct {
		Runs []struct {
			Status     string  `json:"status"`
			Conclusion *string `json:"conclusion"`
		} `json:"check_runs"`
	}
	path := "/repos/" + repository.FullName + "/commits/" + url.PathEscape(sha) + "/check-runs?per_page=100"
	if err := application.request(ctx, http.MethodGet, path, token, nil, &checks); err != nil {
		return "", err
	}
	state := ChecksPassed
	for _, run := range checks.Runs {
		if run.Status != "completed" || run.Conclusion == nil {
			return ChecksPending, nil
		}
		switch *run.Conclusion {
		case "success", "neutral", "skipped":
		default:
			state = ChecksFailed
		}
	}
	var combined struct {
		State      string `json:"state"`
		TotalCount int    `json:"total_count"`
	}
	statusPath := "/repos/" + repository.FullName + "/commits/" + url.PathEscape(sha) + "/status"
	if err := application.request(ctx, http.MethodGet, statusPath, token, nil, &combined); err != nil {
		return "", err
	}
	if combined.TotalCount > 0 {
		switch combined.State {
		case "pending":
			return ChecksPending, nil
		case "failure", "error":
			state = ChecksFailed
		}
	}
	return state, nil
}

func (application *Application) CreateDeployment(ctx context.Context, input CreateDeploymentInput) (Deployment, error) {
	if input.RepositoryID <= 0 || input.Ref == "" || input.Environment == "" || input.PlatformdDeploymentID == "" {
		return Deployment{}, errors.New("GitHub deployment input is incomplete")
	}
	repository, token, err := application.repositoryToken(ctx, input.RepositoryID)
	if err != nil {
		return Deployment{}, err
	}
	request := struct {
		Ref                   string            `json:"ref"`
		Task                  string            `json:"task"`
		AutoMerge             bool              `json:"auto_merge"`
		RequiredContexts      []string          `json:"required_contexts"`
		Environment           string            `json:"environment"`
		Description           string            `json:"description"`
		Payload               map[string]string `json:"payload"`
		TransientEnvironment  bool              `json:"transient_environment"`
		ProductionEnvironment bool              `json:"production_environment"`
	}{
		// platformd resolves the exact SHA and evaluates the configured CI policy
		// before this call. GitHub must neither merge another commit nor repeat a
		// broader required-context check here.
		Ref: input.Ref, Task: "platformd", AutoMerge: false, RequiredContexts: []string{},
		Environment: input.Environment, Description: input.Description,
		Payload:               map[string]string{"platformdDeploymentId": input.PlatformdDeploymentID},
		TransientEnvironment:  input.TransientEnvironment,
		ProductionEnvironment: input.ProductionEnvironment,
	}
	var response struct {
		ID int64 `json:"id"`
	}
	if err := application.request(
		ctx, http.MethodPost, "/repos/"+repository.FullName+"/deployments", token, request, &response,
	); err != nil {
		return Deployment{}, err
	}
	if response.ID <= 0 {
		return Deployment{}, errors.New("GitHub deployment response has no ID")
	}
	return Deployment{ID: response.ID}, nil
}

func (application *Application) CreateDeploymentStatus(ctx context.Context, input CreateDeploymentStatusInput) error {
	if input.RepositoryID <= 0 || input.DeploymentID <= 0 || input.Environment == "" {
		return errors.New("GitHub deployment status input is incomplete")
	}
	switch input.State {
	case DeploymentInProgress, DeploymentSuccess, DeploymentFailure, DeploymentInactive:
	default:
		return errors.New("GitHub deployment status state is invalid")
	}
	repository, token, err := application.repositoryToken(ctx, input.RepositoryID)
	if err != nil {
		return err
	}
	request := struct {
		State          DeploymentStatusState `json:"state"`
		Environment    string                `json:"environment"`
		Description    string                `json:"description"`
		LogURL         string                `json:"log_url,omitempty"`
		EnvironmentURL string                `json:"environment_url,omitempty"`
		AutoInactive   bool                  `json:"auto_inactive"`
	}{
		State: input.State, Environment: input.Environment, Description: input.Description,
		LogURL: input.LogURL, EnvironmentURL: input.EnvironmentURL, AutoInactive: true,
	}
	path := "/repos/" + repository.FullName + "/deployments/" + strconv.FormatInt(input.DeploymentID, 10) + "/statuses"
	return application.request(ctx, http.MethodPost, path, token, request, nil)
}

func (application *Application) CreateIssueComment(ctx context.Context, repositoryID int64, issueNumber int, body string) (IssueComment, error) {
	if repositoryID <= 0 || issueNumber <= 0 || strings.TrimSpace(body) == "" {
		return IssueComment{}, errors.New("GitHub issue comment input is incomplete")
	}
	repository, token, err := application.repositoryToken(ctx, repositoryID)
	if err != nil {
		return IssueComment{}, err
	}
	var response struct {
		ID int64 `json:"id"`
	}
	path := "/repos/" + repository.FullName + "/issues/" + strconv.Itoa(issueNumber) + "/comments"
	if err := application.request(ctx, http.MethodPost, path, token, map[string]string{"body": body}, &response); err != nil {
		return IssueComment{}, err
	}
	if response.ID <= 0 {
		return IssueComment{}, errors.New("GitHub issue comment response has no ID")
	}
	return IssueComment{ID: response.ID}, nil
}

func (application *Application) UpdateIssueComment(ctx context.Context, repositoryID, commentID int64, body string) error {
	if repositoryID <= 0 || commentID <= 0 || strings.TrimSpace(body) == "" {
		return errors.New("GitHub issue comment input is incomplete")
	}
	repository, token, err := application.repositoryToken(ctx, repositoryID)
	if err != nil {
		return err
	}
	path := "/repos/" + repository.FullName + "/issues/comments/" + strconv.FormatInt(commentID, 10)
	return application.request(ctx, http.MethodPatch, path, token, map[string]string{"body": body}, nil)
}

func (application *Application) DownloadArchive(ctx context.Context, repositoryID int64, sha string, destination io.Writer) error {
	repository, token, err := application.repositoryToken(ctx, repositoryID)
	if err != nil {
		return err
	}
	path := "/repos/" + repository.FullName + "/tarball/" + url.PathEscape(sha)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, application.baseURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "platformd")
	response, err := application.client.Do(request)
	if err != nil {
		return fmt.Errorf("download GitHub archive: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GitHub archive returned %d", response.StatusCode)
	}
	written, err := io.Copy(destination, io.LimitReader(response.Body, maximumArchiveBytes+1))
	if err != nil {
		return err
	}
	if written > maximumArchiveBytes {
		return errors.New("GitHub repository archive exceeds 512 MiB")
	}
	return nil
}
