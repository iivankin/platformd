//go:build linux && amd64 && cgo && integration

package managedpostgres

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/state"
)

const (
	postgresIntegrationDataRoot    = "/var/lib/platformd-managedpostgres-integration"
	postgresIntegrationRuntimeRoot = "/run/platformd-managedpostgres-integration"
	postgresIntegrationReleaseRoot = "/var/lib/platformd/releases/current"
)

func TestMain(main *testing.M) {
	if containerengine.InitReexec() {
		os.Exit(0)
	}
	os.Exit(main.Run())
}

type integrationStore struct{ resource state.ManagedPostgres }

func (store integrationStore) ManagedPostgres(_ context.Context, resourceID string) (state.ManagedPostgres, error) {
	if resourceID != store.resource.ID {
		return state.ManagedPostgres{}, state.ErrManagedPostgresNotFound
	}
	return store.resource, nil
}

func (store integrationStore) ManagedPostgresResources(context.Context) ([]state.ManagedPostgres, error) {
	return []state.ManagedPostgres{store.resource}, nil
}

type integrationPublisher struct{ published int }

func (publisher *integrationPublisher) PublishPostgres(state.ManagedPostgres, containerengine.Container) error {
	publisher.published++
	return nil
}

func (*integrationPublisher) WithdrawPostgres(state.ManagedPostgres) error { return nil }

func TestOfficialPostgresProfileRunsOwnerSQLAndPersists(t *testing.T) {
	if os.Getenv("PLATFORMD_MANAGED_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_MANAGED_POSTGRES_INTEGRATION=1 on an isolated delegated root host")
	}
	for _, root := range []string{postgresIntegrationDataRoot, postgresIntegrationRuntimeRoot} {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(postgresIntegrationDataRoot)
		_ = os.RemoveAll(postgresIntegrationRuntimeRoot)
	})
	paths := layout.FromRoots(postgresIntegrationDataRoot, filepath.Join(postgresIntegrationDataRoot, "config"), postgresIntegrationRuntimeRoot, "/tmp/platformd", "/tmp/platformd.service")
	paths.Current = postgresIntegrationReleaseRoot
	for _, directory := range []string{paths.VolumesRoot, paths.LogsRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := cgrouptree.Setup()
	if err != nil {
		t.Fatal(err)
	}
	config := containerengine.ProductionConfig(paths, tree.WorkloadRoot())
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if _, err := containerengine.PrepareStorage(ctx, config); err != nil {
		t.Fatal(err)
	}
	engine, err := containerengine.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	image, err := engine.Pull(ctx, containerengine.PullRequest{Reference: "docker.io/library/postgres:17", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	network, err := engine.CreateNetwork(containerengine.NetworkSpec{
		Name: "platformd-managedpostgres-integration", Interface: "pdmpi0",
		Subnet: "10.89.53.0/24", Gateway: "10.89.53.1",
		Labels: map[string]string{"io.platformd.test": "managed-postgres"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.RemoveNetwork(network.Name) })
	credentials, err := GenerateCredentials("018bcfe5-687b-7fff-bfff-ffffffffffff", nil)
	if err != nil {
		t.Fatal(err)
	}
	resource := state.ManagedPostgres{
		ID: "postgres-integration", ProjectID: "project-integration", ProjectName: "integration", Name: "database",
		ImageTag: "17", ImageDigest: image.Digest, VolumeID: "postgres-volume",
		DatabaseName: credentials.DatabaseName, OwnerUsername: credentials.OwnerUsername,
	}
	publisher := &integrationPublisher{}
	controller, err := NewController(ControllerConfig{
		Store: integrationStore{resource: resource}, Engine: engine, Publisher: publisher, Growth: allowGrowthGate{}, Maintenance: allowMaintenanceGate{}, Admission: admission.New(),
		OwnerPassword:     func(state.ManagedPostgres) (string, error) { return credentials.OwnerPassword, nil },
		BootstrapPassword: func(state.ManagedPostgres) (string, error) { return credentials.BootstrapPassword, nil },
		Placement: func(state.ManagedPostgres) (Placement, error) {
			return Placement{
				NetworkName: network.Name, Gateway: netip.MustParseAddr(network.Gateway),
				DNSSearch: "integration.internal", CgroupParent: filepath.Join(tree.WorkloadRoot(), "postgres-integration"),
			}, nil
		},
		VolumeRoot: paths.VolumesRoot, LogRoot: paths.LogsRoot,
		LogSizeBytes: 1 << 20, LogMaxFiles: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(ctx, resource.ID); err != nil {
		logs, _ := filepath.Glob(filepath.Join(paths.LogsRoot, "postgres", resource.ID, "*.log"))
		var logContent []byte
		if len(logs) != 0 {
			logContent, _ = os.ReadFile(logs[len(logs)-1])
		}
		t.Fatalf("start managed PostgreSQL: %v\n%s", err, logContent)
	}
	result, err := controller.Query(ctx, resource.ID, `
CREATE TABLE people(id bigint PRIMARY KEY, name text NOT NULL);
INSERT INTO people VALUES (1, 'Ada'), (2, 'Grace');
UPDATE people SET name = 'Ada Lovelace' WHERE id = 1;
DELETE FROM people WHERE id = 2;
SELECT id, name FROM people ORDER BY id;`)
	if err != nil {
		t.Fatal(err)
	}
	last := result.Statements[len(result.Statements)-1]
	if last.CommandTag != "SELECT 1" || len(last.Rows) != 1 || last.Rows[0][1].Text != "Ada Lovelace" {
		t.Fatalf("SQL workspace result = %+v", result)
	}
	privileges, err := controller.Query(ctx, resource.ID, `
SELECT rolsuper, rolcreatedb, rolcreaterole, rolreplication
FROM pg_roles WHERE rolname = current_user`)
	if err != nil || len(privileges.Statements) != 1 || len(privileges.Statements[0].Rows) != 1 {
		t.Fatalf("owner privilege query = %+v, %v", privileges, err)
	}
	for _, cell := range privileges.Statements[0].Rows[0] {
		if cell.Text != "f" {
			t.Fatalf("managed owner has elevated privilege: %+v", privileges)
		}
	}
	if err := controller.Stop(ctx, resource.ID); err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(ctx, resource.ID); err != nil {
		t.Fatal(err)
	}
	result, err = controller.Query(ctx, resource.ID, "SELECT name FROM people WHERE id = 1")
	if err != nil || len(result.Statements) != 1 || result.Statements[0].Rows[0][0].Text != "Ada Lovelace" {
		t.Fatalf("persistent query = %+v, %v", result, err)
	}
	if publisher.published != 2 {
		t.Fatalf("publication count = %d, want 2", publisher.published)
	}
	if err := controller.Stop(ctx, resource.ID); err != nil {
		t.Fatal(err)
	}
}
