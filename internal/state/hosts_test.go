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

func TestServiceHostAssignmentRejectsVolumeMove(t *testing.T) {
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
	host, err := store.ConsumeHostJoinToken(context.Background(), ConsumeHostJoinTokenInput{
		TokenID: tokenID, HostID: hostID, AuditEventID: "audit-join", Name: "edge-1",
		PublicIPv4: "203.0.113.40", TokenHMAC: hosttoken.Digest("host", hostID, "unused"),
		ConsumedAtMillis: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if host.Name != "edge-1" || host.PublicIPv4 != "203.0.113.40" {
		t.Fatalf("joined host = %+v", host)
	}

	created, err := store.CreateService(context.Background(), CreateService{
		ID: "service", ProjectID: "project", Name: "api", Enabled: true, HostID: hostID,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22")},
		AuditEventID: "audit-create", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		RequestCorrelationID: "req-create", CreatedAtMillis: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.HostID != hostID {
		t.Fatalf("created host = %q", created.HostID)
	}
	if _, err := store.CreateVolume(context.Background(), CreateVolume{
		Volume: Volume{
			ID: "volume", ProjectID: "project", ServiceID: created.ID,
			Name: "data", CreatedAtMillis: 40,
		},
		AuditEventID: "audit-volume", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", RequestCorrelationID: "req-volume",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = store.UpdateService(context.Background(), UpdateServiceInput{
		ID: "service", ProjectID: "project", Enabled: true, HostID: "",
		Snapshot: created.Snapshot, ExpectedUpdatedMillis: created.UpdatedAtMillis,
		AuditEventID: "audit-move", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		RequestCorrelationID: "req-move", UpdatedAtMillis: 50,
	})
	if err != ErrServiceHostHasVolumes {
		t.Fatalf("move with volume = %v", err)
	}
}
