package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/mailer"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

type mailMetricStub struct{}

func (mailMetricStub) Query(context.Context, state.MetricScope, string, int64, int64, int64) ([]mailer.MetricPoint, error) {
	return []mailer.MetricPoint{}, nil
}

func TestMailSettingsAPIOmitsPasswordAndManagesAlerts(t *testing.T) {
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(context.Background(), state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit",
		ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateService(context.Background(), state.CreateService{
		ID: "service", ProjectID: "project", Name: "api", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine")},
		AuditEventID: "service-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	sent := 0
	application, err := mailer.New(mailer.Config{
		Store: store, Master: cryptobox.MasterKey{1, 2, 3}, InstallationID: "installation-a",
		Metrics: mailMetricStub{},
		Send: func(context.Context, mailer.SMTPConfig, []byte, mailer.Message) error {
			sent++
			return nil
		},
		Now:     func() time.Time { return time.UnixMilli(1_700_000_000_000) },
		OnError: func(error) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, server.Handler(
		server.DefaultMeta("ready"), server.WithMailer(application),
	))

	get := httptest.NewRecorder()
	handler.ServeHTTP(get, projectRequest(http.MethodGet, "/api/v1/settings/mail", ""))
	if get.Code != http.StatusOK || strings.Contains(get.Body.String(), `"password":`) ||
		!strings.Contains(get.Body.String(), `"configured":false`) {
		t.Fatalf("empty mail settings = %d/%s", get.Code, get.Body)
	}

	put := projectRequest(http.MethodPut, "/api/v1/settings/mail/smtp", `{
  "host":"smtp.example.com","port":587,"username":"alerts","password":"secret",
  "fromAddress":"alerts@example.com","fromName":"platformd","encryption":"starttls"
}`)
	put.Header.Set("Origin", "https://admin.example.com")
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusOK || strings.Contains(putResponse.Body.String(), `"password":`) ||
		!strings.Contains(putResponse.Body.String(), `"passwordSet":true`) ||
		!strings.Contains(putResponse.Body.String(), `"host":"smtp.example.com"`) {
		t.Fatalf("SMTP response = %d/%s", putResponse.Code, putResponse.Body)
	}

	testMail := projectRequest(http.MethodPost, "/api/v1/settings/mail/test", `{"to":"ops@example.com"}`)
	testMail.Header.Set("Origin", "https://admin.example.com")
	testResponse := httptest.NewRecorder()
	handler.ServeHTTP(testResponse, testMail)
	if testResponse.Code != http.StatusOK || sent != 1 {
		t.Fatalf("test mail = %d/%s sent=%d", testResponse.Code, testResponse.Body, sent)
	}

	createError := projectRequest(http.MethodPost, "/api/v1/settings/mail/error-alerts", `{
  "name":"Production errors","enabled":true,"recipients":["ops@example.com"],
  "eventTypes":["issue_created","issue_regressed"],"serviceIds":["service"]
}`)
	createError.Header.Set("Origin", "https://admin.example.com")
	errorResponse := httptest.NewRecorder()
	handler.ServeHTTP(errorResponse, createError)
	if errorResponse.Code != http.StatusCreated {
		t.Fatalf("create error alert = %d/%s", errorResponse.Code, errorResponse.Body)
	}
	var created map[string]any
	if err := json.Unmarshal(errorResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	alertID, _ := created["id"].(string)
	if alertID == "" || errorResponse.Header().Get("Location") == "" {
		t.Fatalf("created error alert = %s", errorResponse.Body)
	}

	createMetric := projectRequest(http.MethodPost, "/api/v1/settings/mail/metric-alerts", `{
  "name":"Latency","enabled":true,"recipients":["ops@example.com"],
  "scope":"installation","sql":"SELECT bucket AS time, avg(value) AS value FROM metrics GROUP BY bucket",
  "operator":"gt","threshold":5,"windowSeconds":60
}`)
	createMetric.Header.Set("Origin", "https://admin.example.com")
	metricResponse := httptest.NewRecorder()
	handler.ServeHTTP(metricResponse, createMetric)
	if metricResponse.Code != http.StatusCreated || !strings.Contains(metricResponse.Body.String(), `"firing":false`) {
		t.Fatalf("create metric alert = %d/%s", metricResponse.Code, metricResponse.Body)
	}

	loaded := httptest.NewRecorder()
	handler.ServeHTTP(loaded, projectRequest(http.MethodGet, "/api/v1/settings/mail", ""))
	if loaded.Code != http.StatusOK || strings.Contains(loaded.Body.String(), `"password":`) ||
		!strings.Contains(loaded.Body.String(), `"name":"Production errors"`) ||
		!strings.Contains(loaded.Body.String(), `"name":"Latency"`) ||
		!strings.Contains(loaded.Body.String(), `"id":"service"`) {
		t.Fatalf("loaded mail settings = %d/%s", loaded.Code, loaded.Body)
	}

	deleteError := projectRequest(http.MethodDelete, "/api/v1/settings/mail/error-alerts/"+alertID, "")
	deleteError.Header.Set("Origin", "https://admin.example.com")
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteError)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete error alert = %d/%s", deleteResponse.Code, deleteResponse.Body)
	}
}
