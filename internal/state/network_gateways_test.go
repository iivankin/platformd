package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
)

func TestNetworkGatewaysAllocateInternalEndpointsAndConnectExports(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(ctx, CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit",
		ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateService(ctx, CreateService{
		ID: "api", ProjectID: "project", Name: "api", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine")},
		AuditEventID: "service-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	create := func(id, name string, configuration NetworkGatewayConfiguration, timestamp int64) (NetworkGateway, error) {
		configuration.Name = name
		return store.CreateNetworkGateway(ctx, CreateNetworkGateway{
			ID: id, ProjectID: "project", Configuration: configuration,
			AuditEventID: id + "-audit", ActorKind: "access", ActorID: "actor",
			ActorEmail: "admin@example.com", CreatedAtMillis: timestamp,
		})
	}
	imported, err := create("warehouse", "warehouse", NetworkGatewayConfiguration{
		Mode: "import", Transport: "vpc", Protocol: "tcp", InterfaceName: "wg0",
		SourceAddress: "10.20.0.2", ListenPort: 5432, RemoteHost: "10.20.0.9", RemotePort: 5432,
	}, 3)
	if err != nil || imported.InternalSlot != firstGatewaySlot {
		t.Fatalf("imported gateway = %+v, %v", imported, err)
	}
	second, err := create("cache", "remote-cache", NetworkGatewayConfiguration{
		Mode: "import", Transport: "mesh", Protocol: "tcp", InterfaceName: "tailscale0",
		SourceAddress: "100.64.0.10", ListenPort: 6379, RemoteHost: "100.64.0.20", RemotePort: 6379,
	}, 4)
	if err != nil || second.InternalSlot != firstGatewaySlot+1 {
		t.Fatalf("second imported gateway = %+v, %v", second, err)
	}
	exported, err := create("export", "private-api", NetworkGatewayConfiguration{
		Mode: "export", Transport: "mesh", Protocol: "tcp", InterfaceName: "tailscale0",
		SourceAddress: "100.64.0.10", ListenPort: 8443, TargetServiceID: "api", TargetPort: 8080,
	}, 5)
	if err != nil || exported.InternalSlot != 0 || exported.TargetService != "api" {
		t.Fatalf("exported gateway = %+v, %v", exported, err)
	}
	if exported.InterfaceName != "" || exported.SourceAddress != "" {
		t.Fatalf("managed Mesh gateway persisted host address: %+v", exported)
	}
	if _, err := create("conflict", "duplicate-port", NetworkGatewayConfiguration{
		Mode: "export", Transport: "mesh", Protocol: "tcp", ListenPort: 8443,
		TargetServiceID: "api", TargetPort: 8081,
	}, 6); err == nil {
		t.Fatal("expected a managed Mesh listener conflict")
	}
	canvas, err := store.ProjectCanvas(ctx, "project")
	if err != nil {
		t.Fatal(err)
	}
	connectionFound := false
	for _, connection := range canvas.Connections {
		if connection.SourceID == "export" && connection.TargetID == "api" && len(connection.EnvironmentNames) == 0 {
			connectionFound = true
		}
	}
	if !connectionFound {
		t.Fatalf("export gateway connection missing: %+v", canvas.Connections)
	}
	resources, err := store.ProjectResources(ctx, "project")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, resource := range resources {
		found = found || (resource.ID == imported.ID && resource.Kind == "network_gateway")
	}
	if !found {
		t.Fatalf("import gateway missing from project resources: %+v", resources)
	}
	if _, err := store.NetworkGateway(ctx, "project", "missing"); !errors.Is(err, ErrNetworkGatewayNotFound) {
		t.Fatalf("missing gateway error = %v", err)
	}
}
