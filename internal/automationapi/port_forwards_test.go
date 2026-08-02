package automationapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/automation"
)

func TestAutomationAPICreatesPortForwardTicketWithCLIInstructions(t *testing.T) {
	handler := automationHandler(t, &repositoryStub{})
	request := httptest.NewRequest(
		http.MethodPost,
		"https://admin.example.com/public/api/v1/projects/shop/resources/database/port-forwards",
		strings.NewReader(`{"port":5432,"localPort":15432,"expiresInSeconds":600}`),
	)
	request.Header.Set("Content-Type", "application/json")
	projectID := "project"
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{
		TokenID: "admin-token", Role: "admin", ProjectID: &projectID,
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusCreated {
		t.Fatalf("create port forward = %d/%s", response.Code, body)
	}
	for _, expected := range []string{
		`"id":"port-forward-id"`, `"project":"shop"`, `"resource":"database"`, `"resourceKind":"postgres"`, `"ticket":"pft_`, "raw.githubusercontent.com/iivankin/platformd/main/install.sh", "sh -s -- forward",
		"wss://admin.example.com/public/api/v1/port-forward", "--local-port 15432",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("response does not contain %q: %s", expected, body)
		}
	}
}

func TestAutomationAPIRemovesIDAndKindPortForwardRoute(t *testing.T) {
	handler := automationHandler(t, &repositoryStub{})
	request := httptest.NewRequest(
		http.MethodPost,
		"https://admin.example.com/public/api/v1/projects/project/resources/postgres/database/port-forwards",
		strings.NewReader(`{"port":5432}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{
		TokenID: "admin-token", Role: "admin",
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("old port forward route = %d/%s", response.Code, response.Body)
	}
}
