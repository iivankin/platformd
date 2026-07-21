package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/githubapp"
	"github.com/iivankin/platformd/internal/server"
)

type githubWebhookVerifier struct {
	err       error
	body      string
	signature string
}

func (verifier *githubWebhookVerifier) VerifyWebhook(_ context.Context, body []byte, signature string) error {
	verifier.body = string(body)
	verifier.signature = signature
	return verifier.err
}

func TestGitHubWebhookRejectsInvalidSignature(t *testing.T) {
	t.Parallel()
	verifier := &githubWebhookVerifier{err: errors.New("signature mismatch")}
	handler, err := server.NewGitHubWebhookHandler(server.GitHubWebhookConfig{Verifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, server.GitHubWebhookPath, strings.NewReader(`{"event":"body"}`))
	request.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || verifier.signature != "sha256=invalid" || verifier.body != `{"event":"body"}` {
		t.Fatalf("response/verifier = %d/%q/%q", response.Code, verifier.signature, verifier.body)
	}
}

func TestGitHubWebhookDispatchesVerifiedPush(t *testing.T) {
	t.Parallel()
	verifier := &githubWebhookVerifier{}
	events := make(chan githubapp.PushEvent, 1)
	handler, err := server.NewGitHubWebhookHandler(server.GitHubWebhookConfig{
		Verifier: verifier,
		OnPush: func(_ context.Context, event githubapp.PushEvent) {
			events <- event
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 40)
	body := `{"ref":"refs/heads/main","after":"` + revision + `","repository":{"id":42},"commits":[]}`
	request := httptest.NewRequest(http.MethodPost, server.GitHubWebhookPath, strings.NewReader(body))
	request.Header.Set("X-GitHub-Delivery", "delivery-id")
	request.Header.Set("X-GitHub-Event", "push")
	request.Header.Set("X-Hub-Signature-256", "sha256=verified")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("X-GitHub-Delivery") != "delivery-id" {
		t.Fatalf("response = %d/%v", response.Code, response.Header())
	}
	select {
	case event := <-events:
		if event.RepositoryID != 42 || event.Branch != "main" || event.Revision != revision {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("verified push was not dispatched")
	}
}

func TestGitHubWebhookUsesCheckSuiteAsSingleCICompletionTrigger(t *testing.T) {
	t.Parallel()
	verifier := &githubWebhookVerifier{}
	events := make(chan githubapp.PushEvent, 2)
	handler, err := server.NewGitHubWebhookHandler(server.GitHubWebhookConfig{
		Verifier: verifier,
		OnPush: func(_ context.Context, event githubapp.PushEvent) {
			events <- event
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("b", 40)
	body := `{"action":"completed","repository":{"id":42},"check_suite":{"head_branch":"main","head_sha":"` + revision + `"}}`
	for _, eventName := range []string{"check_run", "check_suite"} {
		request := httptest.NewRequest(http.MethodPost, server.GitHubWebhookPath, strings.NewReader(body))
		request.Header.Set("X-GitHub-Event", eventName)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("%s response = %d", eventName, response.Code)
		}
	}
	select {
	case event := <-events:
		if event.RepositoryID != 42 || event.Revision != revision || !event.ChecksEvent {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("check suite was not dispatched")
	}
	select {
	case event := <-events:
		t.Fatalf("duplicate check event dispatched: %+v", event)
	case <-time.After(50 * time.Millisecond):
	}
}
