package registry

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

func TestDockerPushContractIntegration(t *testing.T) {
	if os.Getenv("PLATFORMD_DOCKER_CONTRACT") != "1" {
		t.Skip("set PLATFORMD_DOCKER_CONTRACT=1 to exercise a local Docker daemon")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker client is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	payloads, err := NewPayloadStore(filepath.Join(t.TempDir(), "registry"))
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewApplication(store, payloads, cryptobox.MasterKey{1, 2, 3}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := application.CreateRepository(ctx, CreateRepositoryInput{
		Name: "contract/docker", PublicPull: true,
		Actor: Actor{Kind: "access", ID: "test", Email: "test@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(application, allowRegistryFailures{}, admission.New())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		username, _, authenticated := request.BasicAuth()
		t.Logf("Docker request %s %s basic=%t username=%q", request.Method, request.URL.RequestURI(), authenticated, username)
		handler.ServeHTTP(response, request)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	target := host + "/contract/docker:integration"
	dockerConfig := t.TempDir()
	t.Cleanup(func() {
		_ = runDocker(context.Background(), dockerConfig, nil, "image", "rm", "-f", target)
	})
	if err := runDocker(ctx, dockerConfig, nil, "pull", "alpine:3.20"); err != nil {
		t.Fatal(err)
	}
	if err := runDocker(ctx, dockerConfig, nil, "tag", "alpine:3.20", target); err != nil {
		t.Fatal(err)
	}
	if err := runDocker(ctx, dockerConfig, strings.NewReader(created.Secret+"\n"), "login", host, "--username", created.Username, "--password-stdin"); err != nil {
		t.Fatal(err)
	}
	if err := runDocker(ctx, dockerConfig, nil, "push", target); err != nil {
		t.Fatal(err)
	}
	manifest, err := application.Manifest(ctx, created.Repository.ID, "integration")
	if err != nil || manifest.Digest == "" {
		t.Fatalf("Docker push did not publish the tag: %+v, %v", manifest, err)
	}
	if err := runDocker(ctx, dockerConfig, nil, "image", "rm", "-f", target); err != nil {
		t.Fatal(err)
	}
	if err := runDocker(ctx, dockerConfig, nil, "logout", host); err != nil {
		t.Fatal(err)
	}
	if err := runDocker(ctx, t.TempDir(), nil, "pull", target); err != nil {
		t.Fatalf("anonymous public pull failed: %v", err)
	}
}

func runDocker(ctx context.Context, config string, input *strings.Reader, arguments ...string) error {
	command := exec.CommandContext(ctx, "docker", arguments...)
	command.Env = append(os.Environ(), "DOCKER_CONFIG="+config)
	if input != nil {
		command.Stdin = input
	}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return &dockerCommandError{arguments: arguments, output: output.String(), err: err}
	}
	return nil
}

type dockerCommandError struct {
	arguments []string
	output    string
	err       error
}

func (err *dockerCommandError) Error() string {
	return "docker " + strings.Join(err.arguments, " ") + ": " + err.err.Error() + ": " + err.output
}

func (err *dockerCommandError) Unwrap() error { return err.err }
