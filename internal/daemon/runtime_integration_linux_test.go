//go:build linux && integration

package daemon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/projectnetwork"
	"github.com/iivankin/platformd/internal/state"
	"golang.org/x/sys/unix"
)

const daemonIntegrationImage = "docker.io/library/alpine@sha256:7c8cb692ae09657cbc4a3f3cbd0e8d5a2690ba38386aaaf252dbb060bf5eb2e6"

func TestMain(m *testing.M) {
	if containerengine.InitReexec() {
		return
	}
	os.Exit(m.Run())
}

func TestRuntimeStartupRecreatesTransientStateAndKeepsImageCache(t *testing.T) {
	if os.Getenv("PLATFORMD_RUNTIME_INTEGRATION") != "1" || os.Getenv("PLATFORMD_CGROUP_INTEGRATION") != "1" {
		t.Skip("set runtime and cgroup integration flags inside an isolated delegated systemd unit")
	}
	tree, err := cgrouptree.Setup()
	if err != nil {
		t.Fatal(err)
	}
	paths := layout.FromRoots(
		"/var/lib/platformd-daemon-integration",
		"/etc/platformd-daemon-integration",
		"/run/platformd-daemon-integration",
		"/tmp/platformd-daemon-integration",
		"/tmp/platformd-daemon-integration.service",
	)
	for _, root := range []string{paths.DataRoot, paths.ConfigRoot, paths.RuntimeRoot} {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
	}
	if err := os.MkdirAll(paths.ReleasesRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/var/lib/platformd/releases/current", paths.Current); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	projects := []state.RuntimeProject{
		{ID: "integration-a", Name: "alpha", ObjectStoreEnabled: true},
		{ID: "integration-b", Name: "beta"},
	}
	if err := prepareRuntimeHost(ctx, paths, tree.WorkloadRoot()); err != nil {
		t.Fatal(err)
	}
	first, err := startRuntime(ctx, paths, tree.WorkloadRoot(), projects, allowRuntimeGrowth{}, admission.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if len(first.networks) != len(projects)+2 || len(first.projectNetworks) != len(projects) ||
		first.buildNetwork.Name == "" || first.cloudflareMeshNetwork.Name == "" ||
		first.cloudflareMeshNetworkError != nil || len(first.projectFailures) != 0 {
		_ = first.Close()
		t.Fatalf(
			"unexpected network reconcile: networks=%v projects=%v build=%+v mesh=%+v mesh_error=%v failures=%v",
			first.networks, first.projectNetworks, first.buildNetwork, first.cloudflareMeshNetwork,
			first.cloudflareMeshNetworkError, first.projectFailures,
		)
	}
	assertProjectObjectStore(t, ctx, first, paths)
	if err := first.AddProject(state.RuntimeProject{ID: "integration-c", Name: "gamma"}); err != nil {
		_ = first.Close()
		t.Fatalf("live project reconcile: %v", err)
	}
	if len(first.projectNetworks) != 3 || first.dnsZones["integration-c"] == nil {
		_ = first.Close()
		t.Fatalf("live project runtime was not published: %+v", first.projectNetworks)
	}
	if err := first.RemoveProject("integration-c"); err != nil {
		_ = first.Close()
		t.Fatalf("remove live project runtime: %v", err)
	}
	if _, exists := first.projectNetworks["integration-c"]; exists || first.dnsZones["integration-c"] != nil ||
		first.projectDNSServers["integration-c"] != nil {
		_ = first.Close()
		t.Fatalf("removed project runtime remained published: %+v", first.projectNetworks)
	}
	if err := first.AddProject(state.RuntimeProject{ID: "integration-c", Name: "gamma"}); err != nil {
		_ = first.Close()
		t.Fatalf("recreate removed project runtime: %v", err)
	}
	image, err := first.engine.Pull(ctx, containerengine.PullRequest{Reference: daemonIntegrationImage})
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	assertProjectDNS(t, ctx, first, tree, paths, image.ID, "integration-a")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.GeneratedRoot, "stale-secret"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := prepareRuntimeHost(ctx, paths, tree.WorkloadRoot()); err != nil {
		t.Fatal(err)
	}
	second, err := startRuntime(ctx, paths, tree.WorkloadRoot(), projects, allowRuntimeGrowth{}, admission.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.engine.InspectImage(ctx, image.ID); err != nil {
		t.Fatalf("inspect image preserved across startup: %v", err)
	}
	entries, err := os.ReadDir(paths.GeneratedRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("generated runtime state survived restart: %v, %v", entries, err)
	}
}

func assertProjectObjectStore(t *testing.T, ctx context.Context, runtime *runtimeStack, paths layout.Paths) {
	t.Helper()
	store, err := state.Open(ctx, paths.StateDatabase, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "integration-a", Name: "alpha", AuditEventID: "object-project-audit",
		ActorID: "integration", ActorEmail: "integration@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	master := cryptobox.MasterKey{1, 2, 3}
	application, err := objectstore.NewApplication(store, daemonObjectStorageStub{}, master, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.Create(ctx, objectstore.CreateInput{
		ProjectID: "integration-a", Name: "assets", BucketName: "alpha-assets",
		Actor: objectstore.Actor{Kind: "access", ID: "integration", Email: "integration@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dataPlane := &daemonObjectDataPlaneStub{}
	t.Cleanup(dataPlane.close)
	if err := runtime.ConfigureObjectStores(ctx, store, application, dataPlane); err != nil {
		t.Fatal(err)
	}
	if status, message := runtime.ObjectStoreStatus("integration-a"); status != "running" {
		t.Fatalf("object store status = %s: %s", status, message)
	}
}

type daemonObjectStorageStub struct{}

type daemonObjectDataPlaneStub struct {
	server *http.Server
}

func (stub *daemonObjectDataPlaneStub) ConfigureDataPlaneProject(ctx context.Context, _, listenAddress string, _ []objectstore.DataPlaneStore) error {
	// Match the production sidecar: the project bridge is created lazily by netavark.
	listenConfig := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var socketErr error
		if err := raw.Control(func(descriptor uintptr) {
			socketErr = unix.SetsockoptInt(int(descriptor), unix.SOL_IP, unix.IP_FREEBIND, 1)
		}); err != nil {
			return err
		}
		return socketErr
	}}
	listener, err := listenConfig.Listen(ctx, "tcp4", listenAddress)
	if err != nil {
		return err
	}
	stub.server = &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusForbidden)
	})}
	go func() { _ = stub.server.Serve(listener) }()
	return nil
}

func (*daemonObjectDataPlaneStub) RemoveDataPlaneProject(context.Context, string) error { return nil }
func (*daemonObjectDataPlaneStub) BeginDataPlaneQuiesce(context.Context) error          { return nil }
func (*daemonObjectDataPlaneStub) EndDataPlaneQuiesce(context.Context) error            { return nil }
func (*daemonObjectDataPlaneStub) ReconcileBuckets(context.Context, []string) error     { return nil }

func (stub *daemonObjectDataPlaneStub) close() {
	if stub.server != nil {
		_ = stub.server.Close()
	}
}

func (daemonObjectStorageStub) EnsureBucket(context.Context, string) error { return nil }
func (daemonObjectStorageStub) DeleteBucket(context.Context, string) error { return nil }
func (daemonObjectStorageStub) ClearBucket(context.Context, string) error  { return nil }
func (daemonObjectStorageStub) ReconcileBuckets(context.Context, []string) error {
	return nil
}

var errDaemonObjectStorageUnused = errors.New("unused object storage integration operation")

func (daemonObjectStorageStub) Put(context.Context, objectstore.PutInput) (objectstore.ObjectMetadata, error) {
	return objectstore.ObjectMetadata{}, errDaemonObjectStorageUnused
}
func (daemonObjectStorageStub) Object(context.Context, string, string) (objectstore.ObjectMetadata, error) {
	return objectstore.ObjectMetadata{}, errDaemonObjectStorageUnused
}
func (daemonObjectStorageStub) ReadRange(context.Context, string, string, int64, int64, io.Writer) error {
	return errDaemonObjectStorageUnused
}
func (daemonObjectStorageStub) ListEntries(context.Context, string, string, string, string, int) ([]objectstore.ObjectListEntry, bool, error) {
	return nil, false, errDaemonObjectStorageUnused
}
func (daemonObjectStorageStub) Delete(context.Context, string, string) error {
	return errDaemonObjectStorageUnused
}

func assertProjectDNS(t *testing.T, ctx context.Context, runtime *runtimeStack, tree *cgrouptree.Tree, paths layout.Paths, imageID, projectID string) {
	t.Helper()
	network := runtime.projectNetworks[projectID]
	parent, err := tree.Parent("daemon-dns")
	if err != nil {
		t.Fatal(err)
	}
	container, err := runtime.engine.CreateContainer(ctx, containerengine.ContainerSpec{
		ImageID: imageID, Name: "platformd-daemon-dns",
		Command: []string{"/bin/sh", "-c", "sleep 300"},
		Network: network.Name, DNSServers: []string{network.Gateway},
		LogPath:      filepath.Join(paths.LogsRoot, "daemon-dns.log"),
		LogSizeBytes: 1024, LogMaxFiles: 2, CgroupParent: parent,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.engine.RemoveContainer(context.Background(), container.ID, true) })
	if err := runtime.engine.StartContainer(ctx, container.ID); err != nil {
		t.Fatal(err)
	}
	container, err = runtime.engine.InspectContainer(container.ID)
	if err != nil || len(container.IPs[network.Name]) != 1 {
		t.Fatalf("inspect DNS test container: %+v, %v", container, err)
	}
	address, err := netip.ParseAddr(container.IPs[network.Name][0])
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.dnsZones[projectID].Set("api.alpha.internal", address); err != nil {
		t.Fatal(err)
	}
	if err := projectnetwork.MarkBridge(projectID); err != nil {
		t.Fatal(err)
	}
	assertLookup := func(expectedCode int, name, expected string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code, execErr := runtime.engine.ExecContainer(ctx, container.ID, containerengine.ExecRequest{
			Command: []string{"nslookup", name}, Stdout: &stdout, Stderr: &stderr,
		})
		if execErr != nil || code != expectedCode || (expected != "" && !bytes.Contains(stdout.Bytes(), []byte(expected))) {
			t.Fatalf("nslookup %s: code=%d stdout=%q stderr=%q err=%v", name, code, stdout.String(), stderr.String(), execErr)
		}
	}
	assertLookup(0, "api.alpha.internal", address.String())
	assertLookup(0, "assets.alpha.internal", network.Gateway)
	assertLookup(1, "api.beta.internal", "")
	assertLookup(0, "example.com", "")
	var s3Output bytes.Buffer
	s3Code, s3Err := runtime.engine.ExecContainer(ctx, container.ID, containerengine.ExecRequest{
		Command: []string{"/bin/sh", "-c", "wget -S -O /dev/null http://assets.alpha.internal:9000/alpha-assets 2>&1 | grep -q '403 Forbidden'"},
		Stdout:  &s3Output, Stderr: &s3Output,
	})
	if s3Err != nil || s3Code != 0 {
		t.Fatalf("unsigned project S3 request: code=%d output=%q err=%v", s3Code, s3Output.String(), s3Err)
	}
	if err := runtime.engine.RemoveContainer(ctx, container.ID, true); err != nil {
		t.Fatal(err)
	}
}
