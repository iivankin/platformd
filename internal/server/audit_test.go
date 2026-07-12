package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

type auditRepository struct {
	calls int
	query state.AuditQuery
}

func (repository *auditRepository) AuditEvents(_ context.Context, query state.AuditQuery) (state.AuditPage, error) {
	repository.calls++
	repository.query = query
	return state.AuditPage{Events: []state.AuditEvent{{
		ID: "event", ActorKind: "token", ActorID: "token", Action: "server.exec",
		TargetKind: "server", TargetID: "host", Result: "succeeded",
		Metadata: map[string]any{"durationMillis": float64(10)}, CreatedAtMillis: 20,
	}}}, nil
}

func TestAuditAPIRequiresAccessAndPassesExactFilters(t *testing.T) {
	repository := &auditRepository{}
	direct := server.Handler(server.DefaultMeta("ready"), server.WithAudit(repository))
	response := httptest.NewRecorder()
	direct.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil))
	if response.Code != http.StatusForbidden || repository.calls != 0 {
		t.Fatalf("unauthenticated audit = %d/%s calls=%d", response.Code, response.Body, repository.calls)
	}

	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/audit?actorKind=token&action=server.exec&result=succeeded&limit=25", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"action":"server.exec"`) || repository.calls != 1 {
		t.Fatalf("audit response = %d/%s calls=%d", response.Code, response.Body, repository.calls)
	}
	if repository.query.ActorKind != "token" || repository.query.Action != "server.exec" || repository.query.Result != "succeeded" || repository.query.Limit != 25 {
		t.Fatalf("audit query = %+v", repository.query)
	}
}
