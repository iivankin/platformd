package state

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/hosttoken"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/serviceconfig"
)

func TestLookupInternalNamePlacesServicesAndPrimaryResources(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec(`INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	tokenID, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	plaintext, secret, err := hosttoken.GenerateJoin(tokenID, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateHostJoinToken(context.Background(), CreateHostJoinTokenInput{
		ID: tokenID, Name: "edge-1", TokenHMAC: hosttoken.Digest("join", tokenID, secret),
		ExpiresAtMillis: 100_000, AuditEventID: "audit-token", ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", RequestCorrelationID: "req-token", CreatedAtMillis: 10,
	}); err != nil {
		t.Fatal(err)
	}
	hostID, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	parsedID, parsedSecret, err := hosttoken.ParseJoin(plaintext)
	if err != nil || parsedID != tokenID || parsedSecret != secret {
		t.Fatalf("parse join token = %q %v", parsedID, err)
	}
	if _, err := store.ConsumeHostJoinToken(context.Background(), ConsumeHostJoinTokenInput{
		TokenID: tokenID, HostID: hostID, AuditEventID: "audit-join", Name: "edge-1",
		PublicIPv4: "203.0.113.40", TokenHMAC: hosttoken.Digest("host", hostID, "unused"),
		ConsumedAtMillis: 20,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateService(context.Background(), CreateService{
		ID: "api", ProjectID: "project", Name: "api", Enabled: true, HostID: hostID,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22")},
		AuditEventID: "audit-api", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		RequestCorrelationID: "req-api", CreatedAtMillis: 30,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateService(context.Background(), CreateService{
		ID: "web", ProjectID: "project", Name: "web", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22")},
		AuditEventID: "audit-web", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		RequestCorrelationID: "req-web", CreatedAtMillis: 31,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.database.Exec(`
INSERT INTO object_stores(id, project_id, name, bucket_name, cors_origins_json, created_at, updated_at)
VALUES ('store', 'project', 'blobs', 'blobs', '[]', 40, 40)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.database.Exec(`
INSERT INTO analytics_trackers(id, project_id, name, root_domain, created_at, updated_at)
VALUES ('tracker', 'project', 'shop', 'shop.example', 50, 50)`); err != nil {
		t.Fatal(err)
	}

	child, err := store.LookupInternalName(context.Background(), "api.shop.internal")
	if err != nil || !child.Found || child.HostID != hostID {
		t.Fatalf("child service = %+v %v", child, err)
	}
	primary, err := store.LookupInternalName(context.Background(), "web.shop.internal")
	if err != nil || !primary.Found || primary.HostID != "" {
		t.Fatalf("primary service = %+v %v", primary, err)
	}
	errorsName, err := store.LookupInternalName(context.Background(), "errors-api.shop.internal")
	if err != nil || !errorsName.Found || errorsName.HostID != "" {
		t.Fatalf("errors alias must stay on the primary: %+v %v", errorsName, err)
	}
	otelName, err := store.LookupInternalName(context.Background(), "otel-api.shop.internal")
	if err != nil || !otelName.Found || otelName.HostID != "" {
		t.Fatalf("otel alias must stay on the primary: %+v %v", otelName, err)
	}
	blobs, err := store.LookupInternalName(context.Background(), "blobs.shop.internal")
	if err != nil || !blobs.Found || blobs.HostID != "" {
		t.Fatalf("object store = %+v %v", blobs, err)
	}
	analytics, err := store.LookupInternalName(context.Background(), "analytics-shop.shop.internal")
	if err != nil || !analytics.Found || analytics.HostID != "" {
		t.Fatalf("analytics = %+v %v", analytics, err)
	}
	missing, err := store.LookupInternalName(context.Background(), "missing.shop.internal")
	if err != nil || missing.Found {
		t.Fatalf("missing = %+v %v", missing, err)
	}
}
