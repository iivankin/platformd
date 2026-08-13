package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iivankin/platformd/internal/access"
)

type telemetryAccessVerifier struct{}

func (telemetryAccessVerifier) Verify(context.Context, string) (access.Identity, error) {
	return access.Identity{Subject: "subject", Email: "admin@example.com"}, nil
}

type artifactTokenRepository struct {
	ServiceTelemetryRepository
}

func (artifactTokenRepository) RotateServiceArtifactToken(context.Context, string, string) (string, error) {
	return "ptel_artifact_secret", nil
}

func TestRotateServiceArtifactTokenDoesNotRequireAnEmptyJSONBody(t *testing.T) {
	t.Parallel()
	handler := access.ProtectAdmin(
		"admin.example.com",
		telemetryAccessVerifier{},
		rotateServiceArtifactToken(artifactTokenRepository{}),
	)
	request := httptest.NewRequest(http.MethodPost, "https://admin.example.com/rotate", nil)
	request.Host = "admin.example.com"
	request.TLS = &tls.ConnectionState{ServerName: "admin.example.com"}
	request.Header.Set("Cf-Access-Jwt-Assertion", "assertion")
	request.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["authToken"] != "ptel_artifact_secret" {
		t.Fatalf("artifact token = %q", body["authToken"])
	}
}
