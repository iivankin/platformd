//go:build linux && amd64 && cgo && integration

package managedredis

import (
	"bytes"
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/state"
)

const (
	integrationDataRoot    = "/var/lib/platformd-managedredis-integration"
	integrationRuntimeRoot = "/run/platformd-managedredis-integration"
	integrationReleaseRoot = "/var/lib/platformd/releases/current"
)

func TestMain(main *testing.M) {
	if containerengine.InitReexec() {
		os.Exit(0)
	}
	os.Exit(main.Run())
}

type integrationStore struct{ resource state.ManagedRedis }

func (store integrationStore) ManagedRedis(_ context.Context, id string) (state.ManagedRedis, error) {
	if id != store.resource.ID {
		return state.ManagedRedis{}, state.ErrManagedRedisNotFound
	}
	return store.resource, nil
}

func (store integrationStore) ManagedRedisResources(context.Context) ([]state.ManagedRedis, error) {
	return []state.ManagedRedis{store.resource}, nil
}

type integrationPublisher struct{ published int }

func (publisher *integrationPublisher) PublishRedis(state.ManagedRedis, containerengine.Container) error {
	publisher.published++
	return nil
}

func (*integrationPublisher) WithdrawRedis(state.ManagedRedis) error { return nil }

func TestOfficialRedisProfilePersistsRDBAcrossRuntimeRecreation(t *testing.T) {
	if os.Getenv("PLATFORMD_MANAGED_REDIS_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_MANAGED_REDIS_INTEGRATION=1 on an isolated delegated root host")
	}
	for _, root := range []string{integrationDataRoot, integrationRuntimeRoot} {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(integrationDataRoot)
		_ = os.RemoveAll(integrationRuntimeRoot)
	})
	paths := layout.FromRoots(integrationDataRoot, filepath.Join(integrationDataRoot, "config"), integrationRuntimeRoot, "/tmp/platformd", "/tmp/platformd.service")
	paths.Current = integrationReleaseRoot
	for _, directory := range []string{paths.GeneratedRoot, paths.VolumesRoot, paths.LogsRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := cgrouptree.Setup()
	if err != nil {
		t.Fatal(err)
	}
	config := containerengine.ProductionConfig(paths, tree.WorkloadRoot())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := containerengine.PrepareStorage(ctx, config); err != nil {
		t.Fatal(err)
	}
	engine, err := containerengine.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	image, err := engine.Pull(ctx, containerengine.PullRequest{Reference: "docker.io/library/redis:7.4", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	network, err := engine.CreateNetwork(containerengine.NetworkSpec{
		Name: "platformd-managedredis-integration", Interface: "pdmri0",
		Subnet: "10.89.52.0/24", Gateway: "10.89.52.1",
		Labels: map[string]string{"io.platformd.test": "managed-redis"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.RemoveNetwork(network.Name) })
	password, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	resource := state.ManagedRedis{
		ID: "redis-integration", ProjectID: "project-integration", ProjectName: "integration", Name: "cache",
		ImageTag: "7.4", ImageDigest: image.Digest, VolumeID: "redis-volume",
	}
	publisher := &integrationPublisher{}
	controller, err := NewController(Config{
		Store: integrationStore{resource: resource}, Engine: engine, Publisher: publisher,
		Password: func(state.ManagedRedis) (string, error) { return password, nil },
		Placement: func(state.ManagedRedis) (Placement, error) {
			return Placement{NetworkName: network.Name, Gateway: mustAddr(network.Gateway), DNSSearch: "integration.internal", CgroupParent: filepath.Join(tree.WorkloadRoot(), "redis-integration")}, nil
		},
		GeneratedRoot: paths.GeneratedRoot, VolumeRoot: paths.VolumesRoot, LogRoot: paths.LogsRoot,
		LogSizeBytes: 1 << 20, LogMaxFiles: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(ctx, resource.ID); err != nil {
		t.Fatal(err)
	}
	container, active, err := controller.Status(resource.ID)
	if err != nil || !active || container.State != "running" {
		t.Fatalf("managed Redis status = %+v/%v/%v", container, active, err)
	}
	var output bytes.Buffer
	code, err := engine.ExecContainer(ctx, container.ID, containerengine.ExecRequest{
		Command:     []string{"redis-cli", "SET", "platformd-integration", "persisted"},
		Environment: map[string]string{"REDISCLI_AUTH": password}, Stdout: &output,
	})
	if err != nil || code != 0 || output.String() != "OK\n" {
		t.Fatalf("SET result: code=%d output=%q err=%v", code, output.String(), err)
	}
	page, err := controller.ScanKeys(ctx, resource.ID, ScanQuery{Match: "platformd-*", Count: 10})
	if err != nil || len(page.Keys) != 1 || string(page.Keys[0].Key) != "platformd-integration" || page.Keys[0].Type != "string" || page.Keys[0].SizeBytes <= 0 {
		t.Fatalf("SCAN browser result = %+v, %v", page, err)
	}
	preview, err := controller.PreviewKey(ctx, resource.ID, PreviewQuery{Key: page.Keys[0].Key})
	if err != nil || preview.Type != "string" || preview.Length != 9 || len(preview.Items) != 1 || string(preview.Items[0].Values[0]) != "persisted" {
		t.Fatalf("value preview = %+v, %v", preview, err)
	}
	if err := controller.Stop(ctx, resource.ID); err != nil {
		t.Fatal(err)
	}
	rdbPath := filepath.Join(paths.VolumesRoot, resource.ProjectID, resource.VolumeID, "dump.rdb")
	info, err := os.Stat(rdbPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatalf("final RDB = %+v, %v", info, err)
	}
	if err := controller.Start(ctx, resource.ID); err != nil {
		t.Fatal(err)
	}
	container, active, err = controller.Status(resource.ID)
	if err != nil || !active {
		t.Fatalf("restored managed Redis status = %+v/%v/%v", container, active, err)
	}
	output.Reset()
	code, err = engine.ExecContainer(ctx, container.ID, containerengine.ExecRequest{
		Command:     []string{"redis-cli", "GET", "platformd-integration"},
		Environment: map[string]string{"REDISCLI_AUTH": password}, Stdout: &output,
	})
	if err != nil || code != 0 || output.String() != "persisted\n" {
		t.Fatalf("GET result: code=%d output=%q err=%v", code, output.String(), err)
	}
	if publisher.published != 2 {
		t.Fatalf("publication count = %d, want 2", publisher.published)
	}
	if err := controller.Stop(ctx, resource.ID); err != nil {
		t.Fatal(err)
	}
}

func mustAddr(value string) netip.Addr {
	return netip.MustParseAddr(value)
}
