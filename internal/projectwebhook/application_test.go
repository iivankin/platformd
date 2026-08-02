package projectwebhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

type deliveryStore struct {
	project state.ProjectSummary
}

func (store *deliveryStore) Project(context.Context, string) (state.ProjectSummary, error) {
	return store.project, nil
}

func (*deliveryStore) ProjectWebhooks(context.Context, string) ([]state.ProjectWebhook, error) {
	return nil, nil
}

func (*deliveryStore) CreateProjectWebhook(context.Context, state.ProjectWebhookMutation) (state.ProjectWebhook, error) {
	return state.ProjectWebhook{}, nil
}

func (*deliveryStore) UpdateProjectWebhook(context.Context, state.ProjectWebhookMutation) (state.ProjectWebhook, error) {
	return state.ProjectWebhook{}, nil
}

func (*deliveryStore) DeleteProjectWebhook(context.Context, state.DeleteProjectWebhook) error {
	return nil
}

func TestWebhookTestRetriesTransientFailuresThreeTimes(t *testing.T) {
	t.Parallel()
	attempts := 0
	var received Event
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		attempts++
		if request.Header.Get("X-Platformd-Event") != EventWebhookTest || request.Header.Get("X-Platformd-Delivery") != "delivery-id" {
			t.Errorf("unexpected delivery headers: %q/%q", request.Header.Get("X-Platformd-Event"), request.Header.Get("X-Platformd-Delivery"))
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Errorf("decode delivery: %v", err)
		}
		if attempts <= 3 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	application, err := New(Config{
		Store: &deliveryStore{project: state.ProjectSummary{ID: "project", Name: "storefront"}},
		NewID: func() (string, error) { return "delivery-id", nil },
		Now:   func() time.Time { return time.Unix(1_700_000_000, 0) },
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := application.Test(context.Background(), "project", server.URL); err != nil {
		t.Fatal(err)
	}
	if attempts != 4 {
		t.Fatalf("delivery attempts = %d, want 4", attempts)
	}
	if received.ID != "delivery-id" || received.Type != EventWebhookTest || received.Project.ID != "project" || received.Project.Name != "storefront" {
		t.Fatalf("unexpected webhook payload: %+v", received)
	}
}

func TestWebhookTestDoesNotRetryPermanentClientFailure(t *testing.T) {
	t.Parallel()
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		attempts++
		response.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	application, err := New(Config{
		Store: &deliveryStore{project: state.ProjectSummary{ID: "project", Name: "storefront"}},
		NewID: func() (string, error) { return "delivery-id", nil },
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := application.Test(context.Background(), "project", server.URL); err == nil {
		t.Fatal("permanent client failure succeeded")
	}
	if attempts != 1 {
		t.Fatalf("delivery attempts = %d, want 1", attempts)
	}
}
