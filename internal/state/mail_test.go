package state_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

func TestSMTPSettingsStayWriteOnlyAtTheStore(t *testing.T) {
	store := openMailStore(t)
	if _, err := store.SMTPSettings(context.Background()); !errors.Is(err, state.ErrSMTPNotConfigured) {
		t.Fatalf("empty SMTP = %v", err)
	}
	if err := store.PutSMTPSettings(context.Background(), state.PutSMTPSettingsInput{
		Settings: state.SMTPSettings{
			Host: "smtp.example.com", Port: 587, Username: "alerts",
			PasswordEncrypted: []byte("sealed-password"), FromAddress: "alerts@example.com",
			FromName: "platformd", Encryption: "starttls",
		},
		AuditEventID: "audit-smtp", ActorID: "actor", ActorEmail: "admin@example.com",
		RequestCorrelationID: "request", UpdatedAtMillis: 10,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.SMTPSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Host != "smtp.example.com" || loaded.Port != 587 || loaded.FromAddress != "alerts@example.com" ||
		loaded.Encryption != "starttls" || string(loaded.PasswordEncrypted) != "sealed-password" {
		t.Fatalf("stored SMTP = %+v", loaded)
	}
	var action, targetKind string
	if err := store.QueryRowContext(context.Background(),
		`SELECT action, target_kind FROM audit_events WHERE id = 'audit-smtp'`,
	).Scan(&action, &targetKind); err != nil || action != "smtp.configure" || targetKind != "smtp" {
		t.Fatalf("SMTP audit = %s/%s, %v", action, targetKind, err)
	}
}

func TestMailErrorAlertsRejectMissingServicesAndHonorTheLimit(t *testing.T) {
	store := openMailStore(t)
	createMailProjectService(t, store)
	mutation := state.MailAlertMutation{
		AuditEventID: "audit-error", ActorID: "actor", ActorEmail: "admin@example.com",
	}
	alert := state.MailErrorAlert{
		ID: "error-alert", Name: "Production errors", Enabled: true,
		Recipients: []string{"ops@example.com"}, EventTypes: []string{"issue_created"},
		ServiceIDs: []string{"service"}, CreatedAtMillis: 20, UpdatedAtMillis: 20,
	}
	if err := store.CreateMailErrorAlert(context.Background(), alert, mutation); err != nil {
		t.Fatal(err)
	}
	missing := alert
	missing.ID = "missing-service"
	missing.ServiceIDs = []string{"missing"}
	mutation.AuditEventID = "audit-missing"
	if err := store.CreateMailErrorAlert(context.Background(), missing, mutation); !errors.Is(err, state.ErrMailAlertServiceMissing) {
		t.Fatalf("missing service = %v", err)
	}
	listed, err := store.MailErrorAlerts(context.Background())
	if err != nil || len(listed) != 1 || listed[0].Name != "Production errors" || len(listed[0].ServiceIDs) != 1 {
		t.Fatalf("listed error alerts = %+v, %v", listed, err)
	}
	for index := 0; index < 19; index++ {
		next := alert
		next.ID = "error-alert-" + string(rune('a'+index))
		if err := store.CreateMailErrorAlert(context.Background(), next, state.MailAlertMutation{
			AuditEventID: "audit-limit-" + next.ID, ActorID: "actor", ActorEmail: "admin@example.com",
		}); err != nil {
			t.Fatal(err)
		}
	}
	overflow := alert
	overflow.ID = "overflow"
	if err := store.CreateMailErrorAlert(context.Background(), overflow, state.MailAlertMutation{
		AuditEventID: "audit-overflow", ActorID: "actor", ActorEmail: "admin@example.com",
	}); !errors.Is(err, state.ErrMailAlertLimit) {
		t.Fatalf("alert limit = %v", err)
	}
	services, err := store.MailAlertServices(context.Background())
	if err != nil || len(services) != 1 || services[0].ID != "service" || services[0].ProjectName != "shop" {
		t.Fatalf("mail services = %+v, %v", services, err)
	}
	if err := store.DeleteMailErrorAlert(context.Background(), "error-alert", state.MailAlertMutation{
		AuditEventID: "audit-delete", ActorID: "actor", ActorEmail: "admin@example.com",
	}, 30); err != nil {
		t.Fatal(err)
	}
}

func TestMailMetricAlertEvaluationDoesNotMarkControlDirty(t *testing.T) {
	store := openMailStore(t)
	control := 0
	store.SetControlCommitObserver(func() { control++ })
	alert := state.MailMetricAlert{
		ID: "metric-alert", Name: "Latency", Enabled: true,
		Recipients: []string{"ops@example.com"}, Scope: state.MetricScope{Kind: state.MetricScopeInstallation},
		SQL:      "SELECT bucket AS time, avg(value) AS value FROM metrics GROUP BY bucket",
		Operator: "gt", Threshold: 5, WindowSeconds: 60, CreatedAtMillis: 40, UpdatedAtMillis: 40,
	}
	if err := store.CreateMailMetricAlert(context.Background(), alert, state.MailAlertMutation{
		AuditEventID: "audit-metric", ActorID: "actor", ActorEmail: "admin@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	if control != 1 {
		t.Fatalf("control writes after create = %d", control)
	}
	value := 9.5
	if err := store.RecordMailMetricAlertEvaluation(context.Background(), alert.ID, true, &value, 50, 50, 40); err != nil {
		t.Fatal(err)
	}
	if control != 1 {
		t.Fatalf("control writes after evaluation = %d", control)
	}
	listed, err := store.MailMetricAlerts(context.Background())
	if err != nil || len(listed) != 1 || !listed[0].Firing || listed[0].LastValue == nil || *listed[0].LastValue != value {
		t.Fatalf("evaluated alert = %+v, %v", listed, err)
	}
}

func TestMailMetricAlertEvaluationIgnoresStaleDefinitions(t *testing.T) {
	store := openMailStore(t)
	alert := state.MailMetricAlert{
		ID: "metric-alert", Name: "Latency", Enabled: true,
		Recipients: []string{"ops@example.com"}, Scope: state.MetricScope{Kind: state.MetricScopeInstallation},
		SQL:      "SELECT bucket AS time, avg(value) AS value FROM metrics GROUP BY bucket",
		Operator: "gt", Threshold: 5, WindowSeconds: 60, CreatedAtMillis: 40, UpdatedAtMillis: 40,
	}
	if err := store.CreateMailMetricAlert(context.Background(), alert, state.MailAlertMutation{
		AuditEventID: "audit-metric", ActorID: "actor", ActorEmail: "admin@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	updated := alert
	updated.Name = "Latency 2"
	updated.UpdatedAtMillis = 50
	if err := store.UpdateMailMetricAlert(context.Background(), updated, state.MailAlertMutation{
		AuditEventID: "audit-metric-update", ActorID: "actor", ActorEmail: "admin@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	value := 9.5
	if err := store.RecordMailMetricAlertEvaluation(context.Background(), alert.ID, true, &value, 60, 60, 40); err != nil {
		t.Fatal(err)
	}
	listed, err := store.MailMetricAlerts(context.Background())
	if err != nil || len(listed) != 1 || listed[0].Firing || listed[0].Name != "Latency 2" {
		t.Fatalf("stale evaluation overwrote the updated alert = %+v, %v", listed, err)
	}
}

func openMailStore(t *testing.T) *state.Store {
	t.Helper()
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func createMailProjectService(t *testing.T, store *state.Store) {
	t.Helper()
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
}
