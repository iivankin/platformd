package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/server"
)

type recoveryRepositoryStub struct{ retries int }

func (*recoveryRepositoryStub) RecoveryStatus(context.Context) (server.RecoveryStatus, error) {
	return server.RecoveryStatus{
		Resources: []server.RecoveryResource{{
			ResourceKind: "redis", ResourceID: "redis-1", Status: "restored",
			GenerationID: "generation-1", SourceCompletedAt: 42,
		}},
		LastError: "registry restore failed",
	}, nil
}

func (repository *recoveryRepositoryStub) RetryRecovery() { repository.retries++ }

func TestRecoveryStatusAndRetryRoutes(t *testing.T) {
	repository := &recoveryRepositoryStub{}
	handler := server.Handler(server.DefaultMeta("recovery"), server.WithRecovery(repository))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/recovery", nil))
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"generationId":"generation-1"`) ||
		!strings.Contains(response.Body.String(), `"lastError":"registry restore failed"`) {
		t.Fatalf("recovery status = %d/%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/recovery/retry", nil))
	if response.Code != http.StatusAccepted || repository.retries != 1 {
		t.Fatalf("recovery retry = %d, retries=%d", response.Code, repository.retries)
	}
}
