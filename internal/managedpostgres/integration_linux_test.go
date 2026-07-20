//go:build linux && amd64 && cgo && integration

package managedpostgres

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/internaldns"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/postgresextension"
	"github.com/iivankin/platformd/internal/state"
)

const (
	postgresIntegrationDataRoot    = "/var/lib/platformd-managedpostgres-integration"
	postgresIntegrationRuntimeRoot = "/run/platformd-managedpostgres-integration"
	postgresIntegrationReleaseRoot = "/var/lib/platformd/releases/current"
)

type postgresIntegrationProfile struct {
	name          string
	tag           string
	image         string
	interfaceName string
	subnet        string
	gateway       string
	vector        bool
}

func TestMain(main *testing.M) {
	if containerengine.InitReexec() {
		os.Exit(0)
	}
	os.Exit(main.Run())
}

type integrationStore struct {
	mu         sync.Mutex
	resource   state.ManagedPostgres
	extensions []state.ManagedPostgresExtension
}

func (store *integrationStore) ManagedPostgres(_ context.Context, resourceID string) (state.ManagedPostgres, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if resourceID != store.resource.ID {
		return state.ManagedPostgres{}, state.ErrManagedPostgresNotFound
	}
	return store.resource, nil
}

func (store *integrationStore) ManagedPostgresResources(context.Context) ([]state.ManagedPostgres, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return []state.ManagedPostgres{store.resource}, nil
}

func (store *integrationStore) SwitchManagedPostgresVolume(_ context.Context, input state.SwitchManagedPostgresVolume) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if input.ResourceID != store.resource.ID || input.ExpectedVolumeID != store.resource.VolumeID {
		return errors.New("managed PostgreSQL integration volume pointer changed concurrently")
	}
	store.resource.VolumeID = input.VolumeID
	if input.ImageTag != "" {
		store.resource.ImageTag = input.ImageTag
		store.resource.ImageDigest = input.ImageDigest
	}
	store.resource.UpdatedAtMillis = input.UpdatedAtMillis
	return nil
}

func (store *integrationStore) ManagedPostgresExtensions(context.Context, string) ([]state.ManagedPostgresExtension, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]state.ManagedPostgresExtension(nil), store.extensions...), nil
}

func (store *integrationStore) PutManagedPostgresExtension(_ context.Context, input state.PutManagedPostgresExtension) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.extensions {
		if store.extensions[index].Name == input.Name {
			store.extensions[index].Version = input.Version
			store.extensions[index].RecipeDigest = input.RecipeDigest
			return nil
		}
	}
	store.extensions = append(store.extensions, state.ManagedPostgresExtension{
		PostgresID: input.PostgresID, Name: input.Name, Version: input.Version,
		RecipeDigest: input.RecipeDigest,
	})
	return nil
}

func (store *integrationStore) DeleteManagedPostgresExtension(_ context.Context, _ string, name string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.extensions {
		if store.extensions[index].Name == name {
			store.extensions = append(store.extensions[:index], store.extensions[index+1:]...)
			break
		}
	}
	return nil
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
	profiles := []postgresIntegrationProfile{
		{
			name: "pre-18", tag: "17.10-alpine3.23",
			image:         "docker.io/library/postgres@sha256:8189a1f6e40904781fc9e2612687877791d21679866db58b1de996b31fc312e4",
			interfaceName: "pdmp17", subnet: "10.89.53.0/24", gateway: "10.89.53.1",
		},
		{
			name: "18-plus", tag: "18.4-alpine3.23",
			image:         "docker.io/library/postgres@sha256:2342268e5cf8851c327dcf10fc124283448428059f9b756692b7e3302940d769",
			interfaceName: "pdmp18", subnet: "10.89.54.0/24", gateway: "10.89.54.1",
		},
		{
			name: "18-vector", tag: "18.4-bookworm",
			image:         "docker.io/library/postgres@sha256:16fa100a3a6e92c0556632870455e7f8c6f3df5cefddd67d6b95292732bd7ff0",
			interfaceName: "pdmp18v", subnet: "10.89.55.0/24", gateway: "10.89.55.1", vector: true,
		},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			testOfficialPostgresProfile(t, profile)
		})
	}
}

func testOfficialPostgresProfile(t *testing.T, profile postgresIntegrationProfile) {
	t.Helper()
	dataRoot := filepath.Join(postgresIntegrationDataRoot, profile.name)
	runtimeRoot := filepath.Join(postgresIntegrationRuntimeRoot, profile.name)
	for _, root := range []string{dataRoot, runtimeRoot} {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dataRoot)
		_ = os.RemoveAll(runtimeRoot)
	})
	paths := layout.FromRoots(dataRoot, filepath.Join(dataRoot, "config"), runtimeRoot, "/tmp/platformd", "/tmp/platformd.service")
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
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	if _, err := containerengine.PrepareStorage(ctx, config); err != nil {
		t.Fatal(err)
	}
	engine, err := containerengine.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	image, err := engine.Pull(ctx, containerengine.PullRequest{Reference: profile.image, Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	network, err := engine.CreateNetwork(containerengine.NetworkSpec{
		Name: "platformd-managedpostgres-integration-" + profile.name, Interface: profile.interfaceName,
		Subnet: profile.subnet, Gateway: profile.gateway,
		Labels: map[string]string{"io.platformd.test": "managed-postgres"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.RemoveNetwork(network.Name) })
	gateway := netip.MustParseAddr(network.Gateway)
	if err := firewall.EnableIPv4Forwarding(); err != nil {
		t.Fatal(err)
	}
	firewallManager := firewall.New()
	if err := firewallManager.Apply([]firewall.Project{{
		ID: "managed-postgres-" + profile.name, Bridge: network.Interface,
		Subnet: netip.MustParsePrefix(network.Subnet), Gateway: gateway,
	}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = firewallManager.Clear() })
	upstreams, err := internaldns.ReadUpstreams("/etc/resolv.conf")
	if err != nil {
		t.Fatal(err)
	}
	forwarder, err := internaldns.NewForwardCache(upstreams, []netip.Addr{gateway})
	if err != nil {
		t.Fatal(err)
	}
	zone, err := internaldns.NewZone(nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err := internaldns.NewView(zone, forwarder)
	if err != nil {
		t.Fatal(err)
	}
	dnsServer, err := internaldns.Start(ctx, internaldns.ServerConfig{
		Address: gateway, Port: firewall.DNSPort, FreeBind: true, View: view,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dnsServer.Close() })
	credentials, err := GenerateCredentials("018bcfe5-687b-7fff-bfff-ffffffffffff", nil)
	if err != nil {
		t.Fatal(err)
	}
	resource := state.ManagedPostgres{
		ID: "postgres-integration-" + profile.name, ProjectID: "project-integration", ProjectName: "integration", Name: "database",
		ImageTag: profile.tag, ImageDigest: image.Digest, VolumeID: "postgres-volume-" + profile.name,
		DatabaseName: credentials.DatabaseName, OwnerUsername: credentials.OwnerUsername,
	}
	publisher := &integrationPublisher{}
	store := &integrationStore{resource: resource}
	extensionBuilder, err := postgresextension.New(postgresextension.Config{
		Engine: engine, Growth: allowGrowthGate{}, CacheRoot: paths.PostgresExtensionRoot,
		LogRoot: paths.LogsRoot, LogSizeBytes: 1 << 20, LogMaxFiles: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	controller, err := NewController(ControllerConfig{
		Store: store, Extensions: store, ExtensionBuilder: extensionBuilder,
		Engine: engine, Publisher: publisher, Growth: allowGrowthGate{}, Maintenance: allowMaintenanceGate{}, Admission: admission.New(),
		OwnerPassword:     func(state.ManagedPostgres) (string, error) { return credentials.OwnerPassword, nil },
		BootstrapPassword: func(state.ManagedPostgres) (string, error) { return credentials.BootstrapPassword, nil },
		Placement: func(state.ManagedPostgres) (Placement, error) {
			return Placement{
				NetworkName: network.Name, Gateway: netip.MustParseAddr(network.Gateway),
				DNSSearch: "integration.internal", CgroupParent: filepath.Join(tree.WorkloadRoot(), resource.ID),
			}, nil
		},
		VolumeRoot: paths.VolumesRoot, LogRoot: paths.LogsRoot,
		LogSizeBytes: 1 << 20, LogMaxFiles: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = controller.Stop(cleanupContext, resource.ID)
	})
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
	extensions, err := controller.Extensions(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	var fileFDWAvailable bool
	for _, extension := range extensions {
		if extension.Name == "file_fdw" {
			fileFDWAvailable = true
			break
		}
	}
	if profile.vector {
		var progress []string
		if err := controller.ChangeExtension(ctx, resource.ID, postgresextension.VectorName, true, func(value string) {
			progress = append(progress, value)
		}); err != nil {
			buildLogs, _ := filepath.Glob(filepath.Join(paths.LogsRoot, "postgres-extension-builds", "*.log"))
			var logContent []byte
			if len(buildLogs) != 0 {
				logContent, _ = os.ReadFile(buildLogs[len(buildLogs)-1])
			}
			t.Fatalf("install pgvector: %v progress=%v\n%s", err, progress, logContent)
		}
		result, err = controller.Query(ctx, resource.ID, `
CREATE TABLE embeddings(id bigint PRIMARY KEY, embedding vector(3));
INSERT INTO embeddings VALUES (1, '[1,2,3]'), (2, '[4,5,6]');
SELECT round((embedding <-> '[1,2,3]')::numeric, 3) FROM embeddings WHERE id = 2;`)
		if err != nil || result.Statements[len(result.Statements)-1].Rows[0][0].Text != "5.196" {
			t.Fatalf("pgvector query = %+v, %v", result, err)
		}
		dump, err := controller.OpenBackupDump(ctx, resource.ID)
		if err != nil {
			t.Fatal(err)
		}
		dumpBytes, readErr := io.ReadAll(dump)
		closeErr := dump.Close()
		if readErr != nil || closeErr != nil || len(dumpBytes) == 0 {
			t.Fatalf("pgvector backup = %d bytes, %v/%v", len(dumpBytes), readErr, closeErr)
		}
		if err := controller.RestoreReplace(ctx, resource.ID, bytes.NewReader(dumpBytes), Actor{Kind: "system", ID: "integration"}); err != nil {
			t.Fatalf("restore pgvector backup: %v", err)
		}
		result, err = controller.Query(ctx, resource.ID, "SELECT count(*) FROM embeddings")
		if err != nil || result.Statements[0].Rows[0][0].Text != "2" {
			t.Fatalf("restored pgvector data = %+v, %v", result, err)
		}
		if err := controller.Stop(ctx, resource.ID); err != nil {
			t.Fatal(err)
		}
		images, err := engine.ImagesByLabel(ctx, postgresextension.OwnerLabel+"="+postgresextension.DerivedOwner)
		if err != nil || len(images) != 1 {
			t.Fatalf("derived image list = %+v, %v", images, err)
		}
		if err := engine.RemoveImage(ctx, images[0].ID); err != nil {
			t.Fatal(err)
		}
		if err := controller.Start(ctx, resource.ID); err != nil {
			t.Fatalf("rebuild missing pgvector cache: %v", err)
		}
		result, err = controller.Query(ctx, resource.ID, "SELECT count(*) FROM embeddings")
		if err != nil || result.Statements[0].Rows[0][0].Text != "2" {
			t.Fatalf("data after pgvector cache rebuild = %+v, %v", result, err)
		}
	}
	if !fileFDWAvailable {
		t.Fatal("official PostgreSQL image does not expose file_fdw")
	}
	if err := controller.ChangeExtension(ctx, resource.ID, "file_fdw", true, nil); err != nil {
		t.Fatalf("install untrusted extension through privileged controller: %v", err)
	}
	extensions, err = controller.Extensions(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	var fileFDWInstalled bool
	for _, extension := range extensions {
		if extension.Name == "file_fdw" && extension.InstalledVersion != "" {
			fileFDWInstalled = true
			break
		}
	}
	if !fileFDWInstalled {
		t.Fatal("file_fdw was not installed")
	}
	if err := controller.ChangeExtension(ctx, resource.ID, "file_fdw", false, nil); err != nil {
		t.Fatalf("uninstall untrusted extension through privileged controller: %v", err)
	}
	extensions, err = controller.Extensions(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, extension := range extensions {
		if extension.Name == "file_fdw" && extension.InstalledVersion != "" {
			t.Fatal("file_fdw remained installed")
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
	if publisher.published < 2 {
		t.Fatalf("publication count = %d, want at least 2", publisher.published)
	}
	active, ok := controller.activeRuntime(resource.ID)
	if !ok {
		t.Fatal("managed PostgreSQL runtime is not active after restart")
	}
	var profileOutput, profileError bytes.Buffer
	exitCode, execErr := engine.ExecContainer(ctx, active.container.ID, containerengine.ExecRequest{
		Command: []string{"sh", "-ceu", `test -f "$PGDATA/PG_VERSION"; printf %s "$PGDATA"`},
		Stdout:  &profileOutput, Stderr: &profileError,
	})
	if execErr != nil || exitCode != 0 || profileOutput.String() != "/var/lib/postgresql/data/pgdata" {
		t.Fatalf("PGDATA profile = %q, exit=%d, stderr=%q, error=%v", profileOutput.String(), exitCode, profileError.String(), execErr)
	}
	if err := controller.Stop(ctx, resource.ID); err != nil {
		t.Fatal(err)
	}
}
