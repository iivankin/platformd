package mailer_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/mailer"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

type recordingSender struct {
	mu       sync.Mutex
	messages []mailer.Message
}

func (sender *recordingSender) send(_ context.Context, _ mailer.SMTPConfig, _ []byte, message mailer.Message) error {
	sender.mu.Lock()
	sender.messages = append(sender.messages, message)
	sender.mu.Unlock()
	return nil
}

func (sender *recordingSender) len() int {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return len(sender.messages)
}

func (sender *recordingSender) last() mailer.Message {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.messages) == 0 {
		return mailer.Message{}
	}
	return sender.messages[len(sender.messages)-1]
}

type liveMetricQuery struct {
	points []mailer.MetricPoint
}

func (query *liveMetricQuery) Query(context.Context, state.MetricScope, string, int64, int64, int64) ([]mailer.MetricPoint, error) {
	return query.points, nil
}

func TestMailerConfiguresSMTPWithoutExposingThePassword(t *testing.T) {
	application, store, sender := newTestMailer(t, &liveMetricQuery{})
	smtp, err := application.ConfigureSMTP(context.Background(), mailer.SMTPInput{
		Host: "LOCALHOST", Port: 587, Username: "alerts", Password: []byte("secret"),
		FromAddress: "alerts@example.com", FromName: "platformd", Encryption: "starttls",
	}, mailMutation("smtp-a", 10))
	if err != nil {
		t.Fatal(err)
	}
	if !smtp.Configured || smtp.Host != "localhost" || !smtp.PasswordSet || smtp.FromAddress != "alerts@example.com" {
		t.Fatalf("public SMTP = %+v", smtp)
	}
	settings, err := application.Settings(context.Background())
	if err != nil || !settings.SMTP.PasswordSet || settings.SMTP.Host != "localhost" {
		t.Fatalf("settings SMTP = %+v, %v", settings.SMTP, err)
	}
	stored, err := store.SMTPSettings(context.Background())
	if err != nil || string(stored.PasswordEncrypted) == "secret" {
		t.Fatalf("stored password was not encrypted: %v %q", err, stored.PasswordEncrypted)
	}
	replaced, err := application.ConfigureSMTP(context.Background(), mailer.SMTPInput{
		Host: "smtp.example.com", Port: 587, FromAddress: "alerts@example.com", Encryption: "starttls",
	}, mailMutation("smtp-b", 11))
	if err != nil || replaced.Host != "smtp.example.com" || !replaced.PasswordSet {
		t.Fatalf("password reuse = %+v, %v", replaced, err)
	}
	if err := application.TestSMTP(context.Background(), "ops@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := sender.last(); got.Subject != "[platformd] Test email" || strings.Join(got.To, ",") != "ops@example.com" {
		t.Fatalf("test mail = %+v", got)
	}
	ipv6, err := application.ConfigureSMTP(context.Background(), mailer.SMTPInput{
		Host: "[::1]", Port: 587, FromAddress: "alerts@example.com", Encryption: "starttls",
	}, mailMutation("smtp-ipv6", 12))
	if err != nil || ipv6.Host != "::1" {
		t.Fatalf("IPv6 SMTP host = %+v, %v", ipv6, err)
	}
}

func TestMailerSendsErrorAndMetricAlertTransitions(t *testing.T) {
	query := &liveMetricQuery{points: []mailer.MetricPoint{{TimeUnixNano: "2", Value: 12}}}
	application, _, sender := newTestMailer(t, query)
	if _, err := application.ConfigureSMTP(context.Background(), mailer.SMTPInput{
		Host: "smtp.example.com", Port: 587, Password: []byte("secret"),
		FromAddress: "alerts@example.com", Encryption: "starttls",
	}, mailMutation("smtp", 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := application.CreateErrorAlert(context.Background(), state.MailErrorAlert{
		Name: "Production errors", Enabled: true, Recipients: []string{"ops@example.com"},
		EventTypes: []string{"issue_created"}, ServiceIDs: []string{"service"},
	}, mailMutation("error", 20)); err != nil {
		t.Fatal(err)
	}
	if _, err := application.CreateMetricAlert(context.Background(), state.MailMetricAlert{
		Name: "Latency", Enabled: true, Recipients: []string{"ops@example.com"},
		Scope:    state.MetricScope{Kind: state.MetricScopeInstallation},
		SQL:      "SELECT bucket AS time, avg(value) AS value FROM metrics GROUP BY bucket",
		Operator: "gt", Threshold: 10, WindowSeconds: 60,
	}, mailMutation("metric", 21)); err != nil {
		t.Fatal(err)
	}

	application.EnqueueServiceError("service", []byte(`[{
  "eventType":"issue_created",
  "timestamp":"2026-01-01T00:00:00Z",
  "issue":{"id":"issue-1","title":"panic: boom","level":"error","platform":"go"},
  "event":{"id":"event-1","timestamp":"2026-01-01T00:00:00Z"}
}]`))
	waitForMail(t, sender, 1)
	if !strings.Contains(sender.last().Subject, "New error") || !strings.Contains(sender.last().Body, "shop") {
		t.Fatalf("error mail = %+v", sender.last())
	}

	if err := application.EvaluateMetrics(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := sender.last(); !strings.Contains(got.Subject, "FIRING") || !strings.Contains(got.Body, "Latency") {
		t.Fatalf("firing mail = %+v", got)
	}
	query.points = []mailer.MetricPoint{{TimeUnixNano: "3", Value: 1}}
	if err := application.EvaluateMetrics(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := sender.last(); !strings.Contains(got.Subject, "RESOLVED") {
		t.Fatalf("resolved mail = %+v", got)
	}
	if sender.len() != 3 {
		t.Fatalf("message count = %d", sender.len())
	}
}

func TestMailerIgnoresNonIssueTelemetryEvents(t *testing.T) {
	application, _, sender := newTestMailer(t, &liveMetricQuery{})
	if _, err := application.ConfigureSMTP(context.Background(), mailer.SMTPInput{
		Host: "smtp.example.com", Port: 587, Password: []byte("secret"),
		FromAddress: "alerts@example.com", Encryption: "starttls",
	}, mailMutation("smtp", 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := application.CreateErrorAlert(context.Background(), state.MailErrorAlert{
		Name: "Production errors", Enabled: true, Recipients: []string{"ops@example.com"},
		EventTypes: []string{"issue_created"},
	}, mailMutation("error", 20)); err != nil {
		t.Fatal(err)
	}
	application.EnqueueServiceError("service", []byte(`[{
  "eventType":"event_received",
  "timestamp":"2026-01-01T00:00:00Z",
  "issue":{"id":"issue-1","title":"panic: boom","level":"error","platform":"go"},
  "event":{"id":"event-1","timestamp":"2026-01-01T00:00:00Z"}
}]`))
	time.Sleep(50 * time.Millisecond)
	if sender.len() != 0 {
		t.Fatalf("event_received sent mail = %+v", sender.last())
	}
}

func TestMailerDoesNotMarkMetricAlertsFiringWithoutSMTP(t *testing.T) {
	query := &liveMetricQuery{points: []mailer.MetricPoint{{TimeUnixNano: "2", Value: 12}}}
	application, store, sender := newTestMailer(t, query)
	if _, err := application.CreateMetricAlert(context.Background(), state.MailMetricAlert{
		Name: "Latency", Enabled: true, Recipients: []string{"ops@example.com"},
		Scope:    state.MetricScope{Kind: state.MetricScopeInstallation},
		SQL:      "SELECT bucket AS time, avg(value) AS value FROM metrics GROUP BY bucket",
		Operator: "gt", Threshold: 10, WindowSeconds: 60,
	}, mailMutation("metric", 21)); err != nil {
		t.Fatal(err)
	}
	if err := application.EvaluateMetrics(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sender.len() != 0 {
		t.Fatalf("sent mail without SMTP = %+v", sender.last())
	}
	listed, err := store.MailMetricAlerts(context.Background())
	if err != nil || len(listed) != 1 || listed[0].Firing {
		t.Fatalf("metric alert without SMTP = %+v, %v", listed, err)
	}
}

func newTestMailer(t *testing.T, query mailer.MetricQuerier) (*mailer.Application, *state.Store, *recordingSender) {
	t.Helper()
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
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
	sender := &recordingSender{}
	application, err := mailer.New(mailer.Config{
		Store: store, Master: cryptobox.MasterKey{1, 2, 3}, InstallationID: "installation-a",
		Metrics: query, Send: sender.send, Now: func() time.Time { return time.UnixMilli(1_700_000_000_000) },
		OnError: func(err error) { t.Errorf("mailer: %v", err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	return application, store, sender
}

func mailMutation(auditID string, timestamp int64) mailer.Mutation {
	return mailer.Mutation{
		AuditEventID: auditID, ActorID: "actor", ActorEmail: "admin@example.com",
		CorrelationID: "request", UpdatedAtMillis: timestamp,
	}
}

func waitForMail(t *testing.T, sender *recordingSender, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sender.mu.Lock()
		n := len(sender.messages)
		sender.mu.Unlock()
		if n >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d mail messages", count)
}
