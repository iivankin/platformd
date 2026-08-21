package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
)

func TestServiceTelemetryStateUsesServiceForeignKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec(`INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateService(ctx, CreateService{
		ID: "service", ProjectID: "project", Name: "api", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22")},
		AuditEventID: "audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	hash := bytes.Repeat([]byte{0x42}, 32)
	if err := store.SetServiceArtifactTokenHash(ctx, "service", hash, 3); err != nil {
		t.Fatal(err)
	}
	loadedHash, err := store.ServiceArtifactTokenHash(ctx, "service")
	if err != nil || !bytes.Equal(loadedHash, hash) {
		t.Fatalf("artifact hash = %x, %v", loadedHash, err)
	}
	webhook := ServiceTelemetryWebhook{
		ID: "webhook", ServiceID: "service", URL: "https://example.com/errors",
		EventTypes: []string{"event_received", "issue_created"}, SecretEncrypted: []byte("sealed"),
		Enabled: true, CreatedAtMillis: 4, UpdatedAtMillis: 4,
	}
	if err := store.CreateServiceTelemetryWebhook(ctx, webhook); err != nil {
		t.Fatal(err)
	}
	webhooks, err := store.ServiceTelemetryWebhooks(ctx, "service")
	if err != nil || len(webhooks) != 1 || !bytes.Equal(webhooks[0].SecretEncrypted, webhook.SecretEncrypted) {
		t.Fatalf("webhooks = %+v, %v", webhooks, err)
	}
	if err := store.DeleteServiceTelemetryWebhook(ctx, "service", "webhook"); err != nil {
		t.Fatal(err)
	}
	webhooks, err = store.ServiceTelemetryWebhooks(ctx, "service")
	if err != nil || len(webhooks) != 0 {
		t.Fatalf("deleted webhooks = %+v, %v", webhooks, err)
	}
	serviceScope := MetricScope{Kind: MetricScopeService, ProjectID: "project", ServiceID: "service"}
	chart := MetricChart{
		ID: "chart", Scope: serviceScope, Title: "Queue depth",
		SQL:           "SELECT bucket AS time, avg(value) AS value FROM metrics GROUP BY bucket",
		Visualization: "area", Legend: "Depth", Unit: "{job}",
		CreatedAtMillis: 5, UpdatedAtMillis: 5,
	}
	if err := store.CreateMetricChart(ctx, chart); err != nil {
		t.Fatal(err)
	}
	charts, err := store.MetricCharts(ctx, serviceScope)
	if err != nil || len(charts) != 1 || charts[0].SQL != chart.SQL {
		t.Fatalf("metric charts = %+v, %v", charts, err)
	}
	chart.Title = "Queue depth by queue"
	chart.UpdatedAtMillis = 6
	if err := store.UpdateMetricChart(ctx, chart, 5); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMetricChart(ctx, chart, 5); !errors.Is(err, ErrMetricChartChanged) {
		t.Fatalf("stale metric chart update = %v", err)
	}
	if err := store.DeleteMetricChart(ctx, serviceScope, "chart"); err != nil {
		t.Fatal(err)
	}
	for index, scope := range []MetricScope{
		{Kind: MetricScopeProject, ProjectID: "project"},
		{Kind: MetricScopeInstallation},
	} {
		chart.ID = []string{"project-chart", "installation-chart"}[index]
		chart.Scope = scope
		chart.CreatedAtMillis++
		chart.UpdatedAtMillis = chart.CreatedAtMillis
		if err := store.CreateMetricChart(ctx, chart); err != nil {
			t.Fatal(err)
		}
		serviceIDs, err := store.MetricScopeServiceIDs(ctx, scope)
		if err != nil || len(serviceIDs) != 1 || serviceIDs[0] != "service" {
			t.Fatalf("metric scope services = %v, %v", serviceIDs, err)
		}
	}
}

func TestServiceTelemetryCanShareAnOwnedApplicationDomain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(ctx, CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	var first ServiceDesired
	for index, service := range []CreateService{
		{ID: "service-a", ProjectID: "project", Name: "api", Enabled: true, Snapshot: serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine")}, AuditEventID: "service-audit-a", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2},
		{ID: "service-b", ProjectID: "project", Name: "web", Enabled: true, Snapshot: serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine")}, AuditEventID: "service-audit-b", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 3},
	} {
		created, err := store.CreateService(ctx, service)
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			first = created
		}
	}
	for index, domain := range []struct{ serviceID, hostname string }{
		{"service-a", "api.example.com"},
		{"service-b", "web.example.com"},
	} {
		if _, err := store.AttachServiceDomain(ctx, AttachServiceDomainInput{
			ProjectID: "project", ServiceID: domain.serviceID, Hostname: domain.hostname, TargetPort: 8080,
			AuditEventID: fmt.Sprintf("domain-audit-%d", index), ActorKind: "access", ActorID: "actor",
			ActorEmail: "admin@example.com", CreatedAtMillis: int64(4 + index),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.UpdateServiceTelemetryTunnel(ctx, UpdateServiceTelemetryTunnel{
		ID: "service-a", ProjectID: "project", Path: "/client-report",
		ExpectedUpdatedMillis: first.UpdatedAtMillis, AuditEventID: "missing-domain-tunnel-audit", ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 6,
	}); !errors.Is(err, ErrServiceTelemetryTunnelNeedsDomain) {
		t.Fatalf("browser tunnel without public domain = %v", err)
	}
	updated, err := store.UpdateServiceSentryPublicAccess(ctx, UpdateServiceSentryPublicAccess{
		ID: "service-a", ProjectID: "project", PublicHostname: "api.example.com",
		ExpectedUpdatedMillis: first.UpdatedAtMillis, AuditEventID: "telemetry-audit", ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 6,
	})
	if err != nil || updated.SentryPublicHostname != "api.example.com" {
		t.Fatalf("shared telemetry domain = %q, %v", updated.SentryPublicHostname, err)
	}
	updated, err = store.UpdateServiceTelemetryTunnel(ctx, UpdateServiceTelemetryTunnel{
		ID: "service-a", ProjectID: "project", Path: " /client-report ",
		ExpectedUpdatedMillis: updated.UpdatedAtMillis, AuditEventID: "tunnel-audit", ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 7,
	})
	if err != nil || updated.SentryTunnelPath != "/client-report" {
		t.Fatalf("browser telemetry tunnel = %q, %v", updated.SentryTunnelPath, err)
	}
	resolved, err := store.ServiceBySentryHostname(ctx, "api.example.com")
	if err != nil || resolved.ID != "service-a" {
		t.Fatalf("service telemetry public domain = %q, %v", resolved.ID, err)
	}
	updated, err = store.UpdateServiceOTLPTracePublicAccess(ctx, UpdateServiceOTLPTracePublicAccess{
		ID: "service-a", ProjectID: "project", PublicHostname: "api.example.com", Path: " /otel/v1/traces ",
		ExpectedUpdatedMillis: updated.UpdatedAtMillis, AuditEventID: "otlp-traces-audit", ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 8,
	})
	if err != nil || updated.OTLPTracePublicHostname != "api.example.com" || updated.OTLPTracePath != "/otel/v1/traces" {
		t.Fatalf("public OTLP trace endpoint = %q%s, %v", updated.OTLPTracePublicHostname, updated.OTLPTracePath, err)
	}
	resolved, err = store.ServiceByOTLPTraceHostname(ctx, "api.example.com")
	if err != nil || resolved.ID != "service-a" {
		t.Fatalf("service OTLP trace public domain = %q, %v", resolved.ID, err)
	}
	if _, err := store.UpdateServiceOTLPTracePublicAccess(ctx, UpdateServiceOTLPTracePublicAccess{
		ID: "service-a", ProjectID: "project", PublicHostname: "api.example.com", Path: "/otel/../traces",
		ExpectedUpdatedMillis: updated.UpdatedAtMillis, AuditEventID: "invalid-otlp-traces-audit", ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 9,
	}); !errors.Is(err, ErrServiceOTLPTracePublicAccessInvalid) {
		t.Fatalf("invalid public OTLP trace endpoint = %v", err)
	}
	if _, err := store.UpdateServiceTelemetryTunnel(ctx, UpdateServiceTelemetryTunnel{
		ID: "service-a", ProjectID: "project", Path: "/client/../report",
		ExpectedUpdatedMillis: updated.UpdatedAtMillis, AuditEventID: "invalid-tunnel-audit", ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 10,
	}); !errors.Is(err, ErrServiceTelemetryTunnelPathInvalid) {
		t.Fatalf("invalid browser telemetry tunnel = %v", err)
	}
	if err := store.DetachServiceDomain(ctx, DetachServiceDomainInput{
		ProjectID: "project", ServiceID: "service-a", Hostname: "api.example.com",
		AuditEventID: "detach-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 11,
	}); !errors.Is(err, ErrDomainTelemetryUse) {
		t.Fatalf("detach selected telemetry domain = %v", err)
	}
	if _, err := store.AttachServiceDomain(ctx, AttachServiceDomainInput{
		ProjectID: "project", ServiceID: "service-b", Hostname: "api.example.com", TargetPort: 8080, Move: true,
		AuditEventID: "move-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 12,
	}); !errors.Is(err, ErrDomainTelemetryUse) {
		t.Fatalf("move selected telemetry domain = %v", err)
	}
	if _, err := store.UpdateServiceSentryPublicAccess(ctx, UpdateServiceSentryPublicAccess{
		ID: "service-a", ProjectID: "project", PublicHostname: "web.example.com",
		ExpectedUpdatedMillis: updated.UpdatedAtMillis, AuditEventID: "foreign-domain-audit", ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 13,
	}); !errors.Is(err, ErrHostnameInUse) {
		t.Fatalf("foreign service telemetry domain = %v", err)
	}
	cleared, err := store.UpdateServiceSentryPublicAccess(ctx, UpdateServiceSentryPublicAccess{
		ID: "service-a", ProjectID: "project", PublicHostname: "",
		ExpectedUpdatedMillis: updated.UpdatedAtMillis, AuditEventID: "clear-telemetry-audit", ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 14,
	})
	if err != nil || cleared.SentryPublicHostname != "" || cleared.SentryTunnelPath != "" {
		t.Fatalf("cleared telemetry endpoint = hostname %q, tunnel %q, %v", cleared.SentryPublicHostname, cleared.SentryTunnelPath, err)
	}
}
