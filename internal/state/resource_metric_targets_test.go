package state_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

func TestResourceMetricTargetsReportConfiguredTrafficRoutes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "project", AuditEventID: "project-audit",
		ActorID: "actor", ActorEmail: "actor@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine")}
	for index, serviceID := range []string{"domain", "preview", "listener", "gateway", "plain"} {
		if _, err := store.CreateService(ctx, state.CreateService{
			ID: serviceID, ProjectID: "project", Name: serviceID, Enabled: true, Snapshot: snapshot,
			AuditEventID: "service-audit-" + serviceID, ActorKind: "access",
			ActorID: "actor", ActorEmail: "actor@example.com", CreatedAtMillis: int64(index + 2),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateVolume(ctx, state.CreateVolume{
		Volume:       state.Volume{ID: "plain-volume", ProjectID: "project", ServiceID: "plain", Name: "data", CreatedAtMillis: 9},
		AuditEventID: "volume-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "actor@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachServiceDomain(ctx, state.AttachServiceDomainInput{
		ProjectID: "project", ServiceID: "domain", Hostname: "domain.example.com", TargetPort: 8080,
		AuditEventID: "domain-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "actor@example.com", CreatedAtMillis: 10,
	}); err != nil {
		t.Fatal(err)
	}
	_, snapshotJSON, configHash, err := serviceconfig.Canonical(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `INSERT INTO service_image_revisions(
id, service_id, tag, kind, archive_path, archive_sha256, image_digest,
oidc_metadata_json, status, created_at
) VALUES ('preview-revision', 'preview', 'test', 'preview', '/images/preview.oci', ?, 'sha256:digest', '{}', 'importing', 11)`, strings.Repeat("a", 64))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginPreviewDeployment(ctx, state.BeginPreviewDeployment{
		ID: "preview-route", ServiceID: "preview", Tag: "test", ImageRevisionID: "preview-revision",
		Hostname: "preview.example.com", TargetPort: 8080, ImageDigest: "sha256:digest",
		ImageReference: "oci-archive:/images/preview.oci", ConfigHash: configHash,
		SnapshotJSON: snapshotJSON, CreatedAtMillis: 11, ExpiresAtMillis: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivatePreviewDeployment(ctx, "preview-route", "", nil, 12); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachServiceListener(ctx, state.AttachServiceListenerInput{
		ProjectID: "project", ServiceID: "listener", Protocol: "tcp", PublicPort: 12_345, TargetPort: 8080,
		AuditEventID: "listener-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "actor@example.com", CreatedAtMillis: 13,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateNetworkGateway(ctx, state.CreateNetworkGateway{
		ID: "gateway-route", ProjectID: "project",
		Configuration: state.NetworkGatewayConfiguration{
			Name: "gateway-route", Mode: "export", Transport: "mesh", Protocol: "udp",
			ListenPort: 20_000, TargetServiceID: "gateway", TargetPort: 5353,
		},
		AuditEventID: "gateway-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "actor@example.com", CreatedAtMillis: 14,
	}); err != nil {
		t.Fatal(err)
	}

	targets, err := store.ResourceMetricTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]state.ResourceMetricTarget, len(targets))
	for _, target := range targets {
		byID[target.ResourceID] = target
	}
	if !byID["domain"].HTTPRoute || !byID["preview"].HTTPRoute ||
		!byID["listener"].TCPRoute || !byID["gateway"].UDPRoute {
		t.Fatalf("configured traffic routes = %+v", byID)
	}
	plain := byID["plain"]
	if plain.HTTPRoute || plain.TCPRoute || plain.UDPRoute {
		t.Fatalf("plain service traffic routes = %+v", plain)
	}
	diskTargets, err := store.ResourceMetricDiskTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range diskTargets {
		if target.ResourceID == "plain" {
			if len(target.VolumeIDs) != 1 || target.VolumeIDs[0] != "plain-volume" {
				t.Fatalf("plain service disk target = %+v", target)
			}
			return
		}
	}
	t.Fatal("plain service disk target is missing")
}
