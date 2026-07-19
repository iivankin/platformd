package githubapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

type clientTestRepository struct {
	settings state.GitHubAppSettings
}

func (repository clientTestRepository) GitHubAppSettings(context.Context) (state.GitHubAppSettings, error) {
	return repository.settings, nil
}

func (clientTestRepository) PutGitHubAppSettings(context.Context, state.PutGitHubAppSettingsInput) error {
	return nil
}

func TestCreateDeploymentAndStatusUseInstallationRepository(t *testing.T) {
	var deploymentBody struct {
		Ref                   string            `json:"ref"`
		Task                  string            `json:"task"`
		AutoMerge             bool              `json:"auto_merge"`
		RequiredContexts      []string          `json:"required_contexts"`
		Environment           string            `json:"environment"`
		Payload               map[string]string `json:"payload"`
		TransientEnvironment  bool              `json:"transient_environment"`
		ProductionEnvironment bool              `json:"production_environment"`
	}
	var statusBody struct {
		State          string `json:"state"`
		Environment    string `json:"environment"`
		Description    string `json:"description"`
		LogURL         string `json:"log_url"`
		EnvironmentURL string `json:"environment_url"`
		AutoInactive   bool   `json:"auto_inactive"`
	}
	var createdCommentBody string
	var updatedCommentBody string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/app/installations":
			_, _ = writer.Write([]byte(`[{"id":17}]`))
		case "/app/installations/17/access_tokens":
			_, _ = writer.Write([]byte(`{"token":"installation-token"}`))
		case "/installation/repositories":
			if request.Header.Get("Authorization") != "Bearer installation-token" {
				t.Errorf("repository authorization = %q", request.Header.Get("Authorization"))
			}
			_, _ = writer.Write([]byte(`{"repositories":[{"id":23,"full_name":"acme/api","default_branch":"main"}]}`))
		case "/repos/acme/api/deployments":
			if err := json.NewDecoder(request.Body).Decode(&deploymentBody); err != nil {
				t.Errorf("decode deployment: %v", err)
			}
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":42}`))
		case "/repos/acme/api/deployments/42/statuses":
			if err := json.NewDecoder(request.Body).Decode(&statusBody); err != nil {
				t.Errorf("decode status: %v", err)
			}
			writer.WriteHeader(http.StatusCreated)
		case "/repos/acme/api/issues/19/comments":
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode comment: %v", err)
			}
			createdCommentBody = body["body"]
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":88}`))
		case "/repos/acme/api/issues/comments/88":
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode updated comment: %v", err)
			}
			updatedCommentBody = body["body"]
			writer.WriteHeader(http.StatusOK)
		case "/repos/acme/api/git/trees/main":
			_, _ = writer.Write([]byte(`{"tree":[{"path":"Dockerfile","type":"blob"},{"path":"apps/api","type":"tree"},{"path":"apps/api/Dockerfile","type":"blob"},{"path":"README.md","type":"blob"}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	repository, master := configuredClientTestRepository(t)
	application, err := New(Config{
		Repository: repository, Master: master, InstallationID: "installation",
		HTTPClient: server.Client(), BaseURL: server.URL, Now: func() time.Time { return time.Unix(1000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := application.CreateDeployment(context.Background(), CreateDeploymentInput{
		RepositoryID: 23, Ref: "commit-sha", Environment: "platformd/shop/api",
		Description: "Deploy shop/api with platformd", PlatformdDeploymentID: "local-deployment",
		TransientEnvironment: true, ProductionEnvironment: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != 42 || deploymentBody.Ref != "commit-sha" || deploymentBody.Task != "platformd" || deploymentBody.AutoMerge || len(deploymentBody.RequiredContexts) != 0 || deploymentBody.Payload["platformdDeploymentId"] != "local-deployment" || !deploymentBody.TransientEnvironment || deploymentBody.ProductionEnvironment {
		t.Fatalf("deployment request = id:%d body:%+v", created.ID, deploymentBody)
	}
	if err := application.CreateDeploymentStatus(context.Background(), CreateDeploymentStatusInput{
		RepositoryID: 23, DeploymentID: 42, State: DeploymentSuccess,
		Environment: "platformd/shop/api", Description: "Deployment succeeded",
		LogURL: "https://admin.example.com/deployment", EnvironmentURL: "https://preview.example.com",
	}); err != nil {
		t.Fatal(err)
	}
	if statusBody.State != "success" || statusBody.Environment != "platformd/shop/api" || statusBody.LogURL != "https://admin.example.com/deployment" || statusBody.EnvironmentURL != "https://preview.example.com" || !statusBody.AutoInactive {
		t.Fatalf("status request = %+v", statusBody)
	}
	comment, err := application.CreateIssueComment(context.Background(), 23, 19, "Preview building")
	if err != nil {
		t.Fatal(err)
	}
	if comment.ID != 88 || createdCommentBody != "Preview building" {
		t.Fatalf("created comment = %+v, body %q", comment, createdCommentBody)
	}
	if err := application.UpdateIssueComment(context.Background(), 23, 88, "Preview ready"); err != nil {
		t.Fatal(err)
	}
	if updatedCommentBody != "Preview ready" {
		t.Fatalf("updated comment body = %q", updatedCommentBody)
	}
	paths, err := application.RepositoryPaths(context.Background(), 23, "main", "apps", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0].Path != "apps/api/Dockerfile" || paths[0].Type != "blob" {
		t.Fatalf("repository path suggestions = %+v", paths)
	}
}

func configuredClientTestRepository(t *testing.T) (clientTestRepository, cryptobox.MasterKey) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	box, err := newBox(master, "installation")
	if err != nil {
		t.Fatal(err)
	}
	sealedKey, err := seal(box, "private-key", privateKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	sealedSecret, err := seal(box, "webhook-secret", []byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return clientTestRepository{settings: state.GitHubAppSettings{
		AppID: 11, AppSlug: "platformd", PrivateKeyEncrypted: sealedKey, WebhookSecretEncrypted: sealedSecret,
	}}, master
}
