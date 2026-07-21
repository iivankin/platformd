package servicewatcher

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

type fakeStore struct {
	mu      sync.Mutex
	service state.ServiceDesired
}

func (store *fakeStore) EnabledServiceIDs(context.Context) ([]string, error) {
	return []string{store.service.ID}, nil
}

func (store *fakeStore) DesiredService(context.Context, string) (state.ServiceDesired, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.service, nil
}

type fakeDeployer struct {
	calls chan string
}

func (deployer *fakeDeployer) DeployService(_ context.Context, serviceID string, _ bool) error {
	deployer.calls <- serviceID
	return nil
}

type blockingDeployer struct {
	calls   chan string
	release chan struct{}
	once    sync.Once
}

func (deployer *blockingDeployer) DeployService(_ context.Context, serviceID string, _ bool) error {
	deployer.calls <- serviceID
	deployer.once.Do(func() { <-deployer.release })
	return nil
}

func TestEmbeddedReferenceSleepsUntilExactCommitNotification(t *testing.T) {
	store := &fakeStore{service: state.ServiceDesired{
		ID: "service", Enabled: true,
		Snapshot: serviceconfig.Snapshot{Source: serviceconfig.PlatformRegistrySource("registry.example.com/team/api:latest")},
	}}
	deployer := &fakeDeployer{calls: make(chan string, 4)}
	watcher, err := New(Config{
		Store: store, Deployer: deployer,
		IsEmbedded:             func(string) bool { return true },
		RemoteInterval:         10 * time.Millisecond,
		RemoteMaximumBackoff:   40 * time.Millisecond,
		EmbeddedRetry:          10 * time.Millisecond,
		EmbeddedMaximumBackoff: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := watcher.Start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertNoCall(t, deployer.calls)
	watcher.NotifyEmbedded("registry.example.com/team/other:latest")
	assertNoCall(t, deployer.calls)
	watcher.NotifyEmbedded("registry.example.com/team/api:latest")
	select {
	case serviceID := <-deployer.calls:
		if serviceID != "service" {
			t.Fatalf("service ID = %q", serviceID)
		}
	case <-time.After(time.Second):
		t.Fatal("embedded commit did not wake service")
	}
	assertNoCall(t, deployer.calls)
}

func TestRemoteTagPollsAndDigestReferenceDoesNot(t *testing.T) {
	store := &fakeStore{service: state.ServiceDesired{
		ID: "service", Enabled: true,
		Snapshot: serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("docker.io/library/alpine:latest")},
	}}
	deployer := &fakeDeployer{calls: make(chan string, 4)}
	watcher, err := New(Config{
		Store: store, Deployer: deployer,
		IsEmbedded:             func(string) bool { return false },
		RemoteInterval:         10 * time.Millisecond,
		RemoteMaximumBackoff:   40 * time.Millisecond,
		EmbeddedRetry:          10 * time.Millisecond,
		EmbeddedMaximumBackoff: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := watcher.Start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-deployer.calls:
	case <-time.After(time.Second):
		t.Fatal("remote tag was not polled")
	}
	cancel()

	digestStore := &fakeStore{service: state.ServiceDesired{
		ID: "digest-service", Enabled: true,
		Snapshot: serviceconfig.Snapshot{
			Source: serviceconfig.PublicImageSource("docker.io/library/alpine@sha256:5f70bf18a08660b3c3e431d73e3a1b13f1f4f9f365f22c4b155b87f12ee41a68"),
		},
	}}
	digestDeployer := &fakeDeployer{calls: make(chan string, 1)}
	digestWatcher, err := New(Config{
		Store: digestStore, Deployer: digestDeployer,
		IsEmbedded:             func(string) bool { return false },
		RemoteInterval:         10 * time.Millisecond,
		RemoteMaximumBackoff:   40 * time.Millisecond,
		EmbeddedRetry:          10 * time.Millisecond,
		EmbeddedMaximumBackoff: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	digestContext, digestCancel := context.WithCancel(context.Background())
	defer digestCancel()
	if err := digestWatcher.Start(digestContext, nil); err != nil {
		t.Fatal(err)
	}
	assertNoCall(t, digestDeployer.calls)
}

func TestExponentialDelayIsBounded(t *testing.T) {
	want := []time.Duration{time.Minute, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for failures, expected := range want {
		if actual := exponentialDelay(time.Minute, 15*time.Minute, failures); actual != expected {
			t.Fatalf("failures %d delay = %s, want %s", failures, actual, expected)
		}
	}
}

func TestTrackStopsWatcherAfterServicePinsDigest(t *testing.T) {
	store := &fakeStore{service: state.ServiceDesired{
		ID: "service", Enabled: true,
		Snapshot: serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("docker.io/library/alpine:latest")},
	}}
	deployer := &fakeDeployer{calls: make(chan string, 1)}
	watcher, err := New(Config{
		Store: store, Deployer: deployer,
		IsEmbedded:             func(string) bool { return false },
		RemoteInterval:         time.Hour,
		RemoteMaximumBackoff:   2 * time.Hour,
		EmbeddedRetry:          time.Second,
		EmbeddedMaximumBackoff: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := watcher.Start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.service.Snapshot.Source.Image.Reference = "docker.io/library/alpine@sha256:5f70bf18a08660b3c3e431d73e3a1b13f1f4f9f365f22c4b155b87f12ee41a68"
	store.mu.Unlock()
	if err := watcher.Track(ctx, "service", false); err != nil {
		t.Fatal(err)
	}
	watcher.mu.Lock()
	_, tracked := watcher.services["service"]
	watcher.mu.Unlock()
	if tracked {
		t.Fatal("digest-pinned service is still tracked")
	}
	assertNoCall(t, deployer.calls)
}

func TestReconcileImmediatelyRunsNonAutoUpdatingService(t *testing.T) {
	store := &fakeStore{service: state.ServiceDesired{
		ID: "service", Enabled: true,
		Snapshot: serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("docker.io/library/alpine:3.22")},
	}}
	store.service.Snapshot.Source.AutoUpdate = false
	deployer := &fakeDeployer{calls: make(chan string, 1)}
	watcher, err := New(Config{
		Store: store, Deployer: deployer,
		IsEmbedded:             func(string) bool { return false },
		RemoteInterval:         time.Hour,
		RemoteMaximumBackoff:   2 * time.Hour,
		EmbeddedRetry:          time.Second,
		EmbeddedMaximumBackoff: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := watcher.Start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := watcher.Reconcile(ctx, "service"); err != nil {
		t.Fatal(err)
	}
	select {
	case serviceID := <-deployer.calls:
		if serviceID != "service" {
			t.Fatalf("service ID = %q", serviceID)
		}
	case <-time.After(time.Second):
		t.Fatal("reconcile did not run immediately")
	}
}

func TestReconcileDoesNotLoseMutationWhileOneShotDeploymentRuns(t *testing.T) {
	store := &fakeStore{service: state.ServiceDesired{
		ID: "service", Enabled: true,
		Snapshot: serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("docker.io/library/alpine:3.22")},
	}}
	store.service.Snapshot.Source.AutoUpdate = false
	deployer := &blockingDeployer{calls: make(chan string, 2), release: make(chan struct{})}
	watcher, err := New(Config{
		Store: store, Deployer: deployer,
		IsEmbedded:             func(string) bool { return false },
		RemoteInterval:         time.Hour,
		RemoteMaximumBackoff:   2 * time.Hour,
		EmbeddedRetry:          time.Second,
		EmbeddedMaximumBackoff: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := watcher.Start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := watcher.Reconcile(ctx, "service"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-deployer.calls:
	case <-time.After(time.Second):
		t.Fatal("first reconcile did not start")
	}
	if err := watcher.Reconcile(ctx, "service"); err != nil {
		t.Fatal(err)
	}
	close(deployer.release)
	select {
	case <-deployer.calls:
	case <-time.After(time.Second):
		t.Fatal("mutation queued during deployment was lost")
	}
}

func assertNoCall(t *testing.T, calls <-chan string) {
	t.Helper()
	select {
	case serviceID := <-calls:
		t.Fatalf("unexpected deployment of %s", serviceID)
	case <-time.After(40 * time.Millisecond):
	}
}
