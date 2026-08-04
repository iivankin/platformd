package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/diskpressure"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
)

type fakeStore struct {
	service        state.ServiceDesired
	deployments    map[string]state.BeginDeployment
	failed         map[string]bool
	initialized    map[string]int64
	latestUploaded *state.DeploymentRecord
	latestRevision *state.ImageRevision
}

func (store *fakeStore) DesiredService(context.Context, string) (state.ServiceDesired, error) {
	return store.service, nil
}

func (store *fakeStore) BeginDeployment(_ context.Context, deployment state.BeginDeployment) error {
	store.deployments[deployment.ID] = deployment
	return nil
}

func (store *fakeStore) DiscardDeployment(_ context.Context, deploymentID string) error {
	delete(store.deployments, deploymentID)
	return nil
}

func (store *fakeStore) UpdateDeploymentSource(_ context.Context, deploymentID, digest, reference, revision, message string) error {
	deployment := store.deployments[deploymentID]
	deployment.ImageDigest = digest
	deployment.ImageReference = reference
	deployment.SourceRevision = revision
	deployment.CommitMessage = message
	store.deployments[deploymentID] = deployment
	return nil
}

func (store *fakeStore) FinishDeployment(_ context.Context, deploymentID, status, _, _ string, _ int64) error {
	deployment := store.deployments[deploymentID]
	deployment.Status = status
	store.deployments[deploymentID] = deployment
	if status == "failed" {
		store.failed[deployment.ConfigHash+":"+deployment.ImageDigest] = true
	}
	return nil
}

func (store *fakeStore) ActivateDeployment(_ context.Context, _, deploymentID, expected string, _ int64) error {
	if store.service.ActiveDeploymentID != expected {
		return state.ErrServiceChanged
	}
	deployment := store.deployments[deploymentID]
	store.service.ActiveDeploymentID = deploymentID
	store.service.ActiveImageDigest = deployment.ImageDigest
	store.service.ActiveConfigHash = deployment.ConfigHash
	return nil
}

func (store *fakeStore) FailDeployment(_ context.Context, deploymentID, _, _ string, _ int64) error {
	return store.FinishDeployment(context.Background(), deploymentID, "failed", "", "", 0)
}

func (store *fakeStore) LatestFailedDeployment(_ context.Context, _, configHash, imageDigest string) (bool, error) {
	return store.failed[configHash+":"+imageDigest], nil
}

func (store *fakeStore) LatestUploadedDeployment(_ context.Context, _ string) (state.DeploymentRecord, error) {
	if store.latestUploaded == nil {
		return state.DeploymentRecord{}, state.ErrDeploymentNotFound
	}
	return *store.latestUploaded, nil
}

func (store *fakeStore) LatestReusableProductionRevision(_ context.Context, _ string) (state.ImageRevision, error) {
	if store.latestRevision == nil {
		return state.ImageRevision{}, state.ErrImageRevisionNotFound
	}
	return *store.latestRevision, nil
}

func (store *fakeStore) Deployment(_ context.Context, deploymentID string) (state.DeploymentRecord, error) {
	if store.latestUploaded != nil && store.latestUploaded.ID == deploymentID {
		return *store.latestUploaded, nil
	}
	deployment, ok := store.deployments[deploymentID]
	if !ok {
		return state.DeploymentRecord{}, errors.New("deployment not found")
	}
	var snapshot serviceconfig.Snapshot
	if err := json.Unmarshal(deployment.SnapshotJSON, &snapshot); err != nil {
		return state.DeploymentRecord{}, err
	}
	return state.DeploymentRecord{
		ID: deployment.ID, ServiceID: deployment.ServiceID, ImageDigest: deployment.ImageDigest,
		ImageReference: deployment.ImageReference, ImageRevisionID: deployment.ImageRevisionID,
		SourceRevision: deployment.SourceRevision,
		CommitMessage: deployment.CommitMessage, ConfigHash: deployment.ConfigHash,
		Snapshot: snapshot, Status: "succeeded",
	}, nil
}

func (store *fakeStore) VolumeInitialized(_ context.Context, _, _, volumeID string) (bool, error) {
	_, ok := store.initialized[volumeID]
	return ok, nil
}

func (store *fakeStore) RecordVolumeInitialization(_ context.Context, _, _, volumeID string, initializedAt int64) error {
	if store.initialized == nil {
		store.initialized = make(map[string]int64)
	}
	if _, exists := store.initialized[volumeID]; !exists {
		store.initialized[volumeID] = initializedAt
	}
	return nil
}

type fakeEngine struct {
	events     []string
	created    []containerengine.ContainerSpec
	pulls      []containerengine.PullRequest
	pullImage  *containerengine.Image
	containers map[string]containerengine.Container
	images     map[string]containerengine.Image
	createErr  error
}

func (engine *fakeEngine) Pull(_ context.Context, request containerengine.PullRequest) (containerengine.Image, error) {
	engine.events = append(engine.events, "pull")
	engine.pulls = append(engine.pulls, request)
	image := containerengine.Image{
		ID: "image-id", Digest: "sha256:5f70bf18a08660b3c3e431d73e3a1b13f1f4f9f365f22c4b155b87f12ee41a68",
	}
	if engine.pullImage != nil {
		image = *engine.pullImage
	}
	if engine.images == nil {
		engine.images = make(map[string]containerengine.Image)
	}
	engine.images[image.Digest] = image
	return image, nil
}

func (engine *fakeEngine) InspectImage(_ context.Context, idOrName string) (containerengine.Image, error) {
	image, ok := engine.images[idOrName]
	if !ok {
		return containerengine.Image{}, errors.New("image not found")
	}
	return image, nil
}

func (engine *fakeEngine) CreateContainer(_ context.Context, spec containerengine.ContainerSpec) (containerengine.Container, error) {
	engine.events = append(engine.events, "create:"+spec.Name)
	engine.created = append(engine.created, spec)
	if engine.createErr != nil {
		return containerengine.Container{}, engine.createErr
	}
	container := containerengine.Container{
		ID: spec.Name, Name: spec.Name, State: "created",
		IPs: map[string][]string{spec.Network: {"10.80.0.2"}},
	}
	engine.containers[container.ID] = container
	return container, nil
}

func (engine *fakeEngine) StartContainer(_ context.Context, containerID string) error {
	engine.events = append(engine.events, "start:"+containerID)
	container := engine.containers[containerID]
	container.State = "running"
	engine.containers[containerID] = container
	return nil
}

func (engine *fakeEngine) StopContainer(containerID string, _ uint) error {
	engine.events = append(engine.events, "stop:"+containerID)
	container := engine.containers[containerID]
	container.State = "stopped"
	engine.containers[containerID] = container
	return nil
}

func (engine *fakeEngine) RemoveContainer(_ context.Context, containerID string, _ bool) error {
	engine.events = append(engine.events, "remove:"+containerID)
	delete(engine.containers, containerID)
	return nil
}

func (engine *fakeEngine) InspectContainer(containerID string) (containerengine.Container, error) {
	container, ok := engine.containers[containerID]
	if !ok {
		return containerengine.Container{}, errors.New("container not found")
	}
	return container, nil
}

func (engine *fakeEngine) ExecTerminalContainer(context.Context, string, containerengine.TerminalExecRequest) (int, error) {
	return 0, nil
}

func (engine *fakeEngine) ExecContainer(context.Context, string, containerengine.ExecRequest) (int, error) {
	return 0, nil
}

type fakePublisher struct {
	events []string
}

type credentialResolverFunc func(context.Context, state.ServiceDesired) (ImageCredential, error)

func (resolver credentialResolverFunc) Resolve(ctx context.Context, service state.ServiceDesired) (ImageCredential, error) {
	return resolver(ctx, service)
}

type growthGateFunc func(context.Context) error

func (gate growthGateFunc) PermitGrowth(ctx context.Context) error {
	return gate(ctx)
}

var allowGrowth = growthGateFunc(func(context.Context) error { return nil })

func TestAutomaticRemoteImageUpdateWaitsForMinimumReleaseAge(t *testing.T) {
	const (
		activeDigest    = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		candidateDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	snapshot := serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:latest")}
	snapshot.Source.MinimumReleaseAgeDays = 7
	normalized, _, configHash, err := serviceconfig.Canonical(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{
		service: state.ServiceDesired{
			ID: "service", ProjectID: "project", ProjectName: "shop", Name: "api", Enabled: true,
			Snapshot: normalized, ActiveDeploymentID: "active", ActiveImageDigest: activeDigest, ActiveConfigHash: configHash,
		},
		deployments: make(map[string]state.BeginDeployment), failed: make(map[string]bool),
	}
	candidate := containerengine.Image{
		ID: "candidate", Digest: candidateDigest,
		Created: now.Add(-(6*24*time.Hour + 23*time.Hour)),
	}
	engine := &fakeEngine{containers: make(map[string]containerengine.Container), pullImage: &candidate}
	controller, err := New(Config{
		Store: store, Engine: engine,
		Publisher: &fakePublisher{}, Growth: allowGrowth, Admission: admission.New(),
		Placement: func(state.ServiceDesired) (Placement, error) {
			return Placement{NetworkName: "project-network", Gateway: netip.MustParseAddr("10.80.0.1")}, nil
		},
		LogRoot: filepath.Join(t.TempDir(), "logs"), VolumeRoot: filepath.Join(t.TempDir(), "volumes"),
		LogSizeBytes: 1024, LogMaxFiles: 2, Now: func() time.Time { return now },
		NewID: func() (string, error) { return "poll", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Deploy(context.Background(), "service", false); err != nil {
		t.Fatal(err)
	}
	if len(engine.pulls) != 1 || len(store.deployments) != 0 || len(engine.created) != 0 {
		t.Fatalf("pending update mutated runtime: pulls=%d deployments=%+v containers=%+v", len(engine.pulls), store.deployments, engine.created)
	}
}

func TestMinimumReleaseAgePendingUsesOCICreated(t *testing.T) {
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	if !minimumReleaseAgePending(time.Time{}, now, 7) {
		t.Fatal("image without OCI Created was eligible")
	}
	if !minimumReleaseAgePending(now.Add(-7*24*time.Hour+time.Second), now, 7) {
		t.Fatal("image younger than the minimum age was eligible")
	}
	if minimumReleaseAgePending(now.Add(-7*24*time.Hour), now, 7) {
		t.Fatal("image at the minimum age remained pending")
	}
	if minimumReleaseAgePending(now, now, 0) {
		t.Fatal("disabled minimum age blocked an image")
	}
}

func (publisher *fakePublisher) Publish(service state.ServiceDesired, container containerengine.Container) error {
	publisher.events = append(publisher.events, "publish:"+service.ID+":"+container.ID)
	return nil
}

func (publisher *fakePublisher) Withdraw(service state.ServiceDesired) error {
	publisher.events = append(publisher.events, "withdraw:"+service.ID)
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestStopFirstDeploymentPublishesCandidateAndRestoresOldOnFailure(t *testing.T) {
	store := &fakeStore{
		service: state.ServiceDesired{
			ID: "service", ProjectID: "project", ProjectName: "shop", Name: "api", Enabled: true,
			Snapshot: serviceconfig.Snapshot{
				Source:       serviceconfig.PublicImageSource("alpine:3.22"),
				HealthCheck:  &serviceconfig.HealthCheck{Port: 8080, Path: "/healthz", TimeoutSeconds: 1},
				VolumeMounts: []serviceconfig.VolumeMount{{VolumeID: "volume", ContainerPath: "/data"}},
			},
		},
		deployments: make(map[string]state.BeginDeployment), failed: make(map[string]bool),
	}
	engine := &fakeEngine{containers: make(map[string]containerengine.Container)}
	publisher := &fakePublisher{}
	probeFails := false
	httpClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		if probeFails {
			return nil, errors.New("probe failed")
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(&emptyReader{})}, nil
	})}
	identifierIndex := 0
	identifiers := []string{"deployment-1", "attempt-1", "poll-noop", "deployment-2", "attempt-2", "blocked-deployment"}
	clockIndex := 0
	logRoot := filepath.Join(t.TempDir(), "logs")
	controller, err := New(Config{
		Store: store, Engine: engine, Publisher: publisher, Growth: allowGrowth, Admission: admission.New(),
		Placement: func(state.ServiceDesired) (Placement, error) {
			return Placement{
				NetworkName: "project-network", Gateway: netip.MustParseAddr("10.80.0.1"),
				DNSSearch: "shop.internal", CgroupParent: "/platformd/workloads/service",
			}, nil
		},
		LogRoot: logRoot, VolumeRoot: filepath.Join(t.TempDir(), "volumes"),
		LogSizeBytes: 1024, LogMaxFiles: 2,
		Now: func() time.Time {
			clockIndex++
			return time.Unix(int64(clockIndex*2), 0)
		},
		NewID: func() (string, error) {
			value := identifiers[identifierIndex]
			identifierIndex++
			return value, nil
		},
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Deploy(context.Background(), "service", false); err != nil {
		t.Fatal(err)
	}
	if len(engine.created) != 1 || len(engine.created[0].Entrypoint) != 0 || len(engine.created[0].Command) != 0 || engine.created[0].DNSSearch[0] != "shop.internal" {
		t.Fatalf("candidate spec = %+v", engine.created)
	}
	if len(engine.created[0].ManagedVolumes) != 1 || engine.created[0].ManagedVolumes[0].Destination != "/data" || engine.created[0].ManagedVolumes[0].Initialized {
		t.Fatalf("first deployment managed volumes = %+v", engine.created[0].ManagedVolumes)
	}
	oldContainerID := "platformd-service-deployment-1"
	if store.service.ActiveDeploymentID != "deployment-1" || !slices.Contains(publisher.events, "publish:service:"+oldContainerID) {
		t.Fatalf("initial publication = %+v / %+v", store.service, publisher.events)
	}
	runtimeStatus, active, err := controller.Status("service")
	if err != nil || !active || runtimeStatus.State != "running" || runtimeStatus.DeploymentID != "deployment-1" {
		t.Fatalf("runtime status = %+v, active=%t, error=%v", runtimeStatus, active, err)
	}
	if err := controller.Deploy(context.Background(), "service", false); err != nil {
		t.Fatalf("unchanged image poll = %v", err)
	}
	if len(store.deployments) != 1 {
		t.Fatalf("unchanged image poll created deployment history: %+v", store.deployments)
	}
	if _, err := os.Stat(filepath.Join(logRoot, "services", "service", "poll-noop")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unchanged image poll left build logs: %v", err)
	}

	store.service.Snapshot.Environment = map[string]string{"REVISION": "2"}
	probeFails = true
	err = controller.Deploy(context.Background(), "service", false)
	if err == nil || !strings.Contains(err.Error(), "probe failed") {
		t.Fatalf("second deployment error = %v", err)
	}
	if store.service.ActiveDeploymentID != "deployment-1" || engine.containers[oldContainerID].State != "running" {
		t.Fatalf("old runtime was not restored: %+v / %+v", store.service, engine.containers)
	}
	if len(engine.created) != 2 || len(engine.created[1].ManagedVolumes) != 1 || !engine.created[1].ManagedVolumes[0].Initialized {
		t.Fatalf("second deployment managed volumes = %+v", engine.created)
	}
	secondContainerID := "platformd-service-deployment-2"
	wantEngineOrder := []string{"stop:" + oldContainerID, "start:" + secondContainerID, "start:" + oldContainerID, "remove:" + secondContainerID}
	if !orderedSubset(engine.events, wantEngineOrder) {
		t.Fatalf("engine events = %v, want ordered subset %v", engine.events, wantEngineOrder)
	}
	if err := controller.Deploy(context.Background(), "service", false); !errors.Is(err, ErrBlockedPair) {
		t.Fatalf("blocked retry error = %v", err)
	}
}

func TestRestoreRecreatesExactActiveDeploymentWithoutChangingPointer(t *testing.T) {
	store := &fakeStore{
		service: state.ServiceDesired{
			ID: "service", ProjectID: "project", ProjectName: "shop", Name: "api", Enabled: true,
			Snapshot: serviceconfig.Snapshot{
				Source:      serviceconfig.PrivateImageSource("registry.example.com/acme/api:latest"),
				HealthCheck: &serviceconfig.HealthCheck{Port: 8080, Path: "/healthz", TimeoutSeconds: 1},
			},
		},
		deployments: make(map[string]state.BeginDeployment), failed: make(map[string]bool),
	}
	httpClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(&emptyReader{})}, nil
	})}
	placement := func(state.ServiceDesired) (Placement, error) {
		return Placement{
			NetworkName: "project-network", Gateway: netip.MustParseAddr("10.80.0.1"),
			DNSSearch: "shop.internal", CgroupParent: "/platformd/workloads/service",
		}, nil
	}
	credentials := credentialResolverFunc(func(context.Context, state.ServiceDesired) (ImageCredential, error) {
		return ImageCredential{Username: "robot", Password: "secret"}, nil
	})
	firstEngine := &fakeEngine{containers: make(map[string]containerengine.Container)}
	firstPublisher := &fakePublisher{}
	identifiers := []string{"deployment", "first-attempt"}
	identifierIndex := 0
	first, err := New(Config{
		Store: store, Engine: firstEngine, Publisher: firstPublisher, Credentials: credentials, Growth: allowGrowth, Admission: admission.New(), Placement: placement,
		LogRoot: filepath.Join(t.TempDir(), "logs"), VolumeRoot: filepath.Join(t.TempDir(), "volumes"),
		LogSizeBytes: 1024, LogMaxFiles: 2, HTTPClient: httpClient,
		NewID: func() (string, error) {
			value := identifiers[identifierIndex]
			identifierIndex++
			return value, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Deploy(context.Background(), "service", false); err != nil {
		t.Fatal(err)
	}
	pointer := store.service.ActiveDeploymentID

	restoredEngine := &fakeEngine{containers: make(map[string]containerengine.Container)}
	restoredPublisher := &fakePublisher{}
	restored, err := New(Config{
		Store: store, Engine: restoredEngine, Publisher: restoredPublisher, Credentials: credentials, Growth: allowGrowth, Admission: admission.New(), Placement: placement,
		LogRoot: filepath.Join(t.TempDir(), "restored-logs"), VolumeRoot: filepath.Join(t.TempDir(), "restored-volumes"),
		LogSizeBytes: 1024, LogMaxFiles: 2, HTTPClient: httpClient,
		NewID: func() (string, error) { return "restored-attempt", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Restore(context.Background(), "service"); err != nil {
		t.Fatal(err)
	}
	active, ok := restored.activeContainer("service")
	if !ok || active.deploymentID != pointer || store.service.ActiveDeploymentID != pointer {
		t.Fatalf("restored active/pointer = %+v/%q, want %q", active, store.service.ActiveDeploymentID, pointer)
	}
	if len(restoredEngine.created) != 1 || restoredEngine.created[0].Name != "platformd-service-"+pointer {
		t.Fatalf("restored container specs = %+v", restoredEngine.created)
	}
	if len(restoredEngine.pulls) != 1 || restoredEngine.pulls[0].Username != "robot" || restoredEngine.pulls[0].Password != "secret" {
		t.Fatalf("restored pull authentication = %+v", restoredEngine.pulls)
	}
	prepared, err := restored.PrepareUnexpectedExit(context.Background(), "service", pointer, active.container.ID)
	if err != nil || !prepared {
		t.Fatalf("prepare unexpected exit = %t, %v", prepared, err)
	}
	if _, exists := restored.activeContainer("service"); exists {
		t.Fatal("exited runtime remained active")
	}
	recreated, err := restored.RestoreCurrent(context.Background(), "service", pointer)
	if err != nil || !recreated {
		t.Fatalf("recreate current deployment = %t, %v", recreated, err)
	}
	if store.service.ActiveDeploymentID != pointer {
		t.Fatalf("crash restart changed deployment pointer to %q", store.service.ActiveDeploymentID)
	}
	resume, err := restored.QuiesceAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, active := restored.activeContainer("service"); active {
		t.Fatal("quiesced service remained active")
	}
	containerID := "platformd-service-" + pointer
	if container, exists := restoredEngine.containers[containerID]; !exists || container.State != "stopped" {
		t.Fatalf("quiesced container was removed or running: %+v/%t", container, exists)
	}
	if err := resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if container, active, err := restored.Status("service"); err != nil || !active || container.State != "running" {
		t.Fatalf("resumed service = %+v/%t/%v", container, active, err)
	}
}

func TestCriticalPressureRestoresCachedActiveDigestWithoutPull(t *testing.T) {
	snapshot := serviceconfig.Snapshot{
		Source:      serviceconfig.PublicImageSource("registry.example.com/acme/api:latest"),
		HealthCheck: &serviceconfig.HealthCheck{Port: 8080, Path: "/healthz", TimeoutSeconds: 1},
	}
	normalized, snapshotJSON, configHash, err := serviceconfig.Canonical(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	const digest = "sha256:5f70bf18a08660b3c3e431d73e3a1b13f1f4f9f365f22c4b155b87f12ee41a68"
	store := &fakeStore{
		service: state.ServiceDesired{
			ID: "service", ProjectID: "project", ProjectName: "shop", Name: "api", Enabled: true,
			Snapshot: normalized, ActiveDeploymentID: "deployment", ActiveImageDigest: digest, ActiveConfigHash: configHash,
		},
		deployments: map[string]state.BeginDeployment{
			"deployment": {
				ID: "deployment", ServiceID: "service", ImageDigest: digest,
				ConfigHash: configHash, SnapshotJSON: snapshotJSON,
			},
		},
		failed: make(map[string]bool),
	}
	engine := &fakeEngine{
		containers: make(map[string]containerengine.Container),
		images: map[string]containerengine.Image{
			digest: {ID: "cached-image", Digest: digest},
		},
	}
	publisher := &fakePublisher{}
	controller, err := New(Config{
		Store: store, Engine: engine, Publisher: publisher, Admission: admission.New(),
		Growth: growthGateFunc(func(context.Context) error {
			return fmt.Errorf("%w: critical", diskpressure.ErrGrowthDenied)
		}),
		Placement: func(state.ServiceDesired) (Placement, error) {
			return Placement{
				NetworkName: "project-network", Gateway: netip.MustParseAddr("10.80.0.1"),
				DNSSearch: "shop.internal", CgroupParent: "/platformd/workloads/service",
			}, nil
		},
		LogRoot: filepath.Join(t.TempDir(), "logs"), VolumeRoot: filepath.Join(t.TempDir(), "volumes"),
		LogSizeBytes: 1024, LogMaxFiles: 2,
		NewID: func() (string, error) { return "attempt", nil },
		HTTPClient: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(&emptyReader{})}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Restore(context.Background(), "service"); err != nil {
		t.Fatal(err)
	}
	if len(engine.pulls) != 0 || len(engine.created) != 1 || engine.created[0].ImageID != "cached-image" {
		t.Fatalf("cached restore pulls/specs = %+v/%+v", engine.pulls, engine.created)
	}
	if err := controller.Deploy(context.Background(), "service", false); err != nil {
		t.Fatalf("critical active reconcile = %v", err)
	}
	if len(engine.pulls) != 0 {
		t.Fatalf("critical active reconcile pulled images: %+v", engine.pulls)
	}

	store.service.Snapshot.Environment = map[string]string{"REVISION": "next"}
	if err := controller.Deploy(context.Background(), "service", false); !errors.Is(err, diskpressure.ErrGrowthDenied) {
		t.Fatalf("critical changed deployment = %v", err)
	}
	if len(engine.pulls) != 0 || len(store.deployments) != 1 {
		t.Fatalf("denied deployment mutated state: pulls=%+v deployments=%+v", engine.pulls, store.deployments)
	}
}

type emptyReader struct{}

func (*emptyReader) Read([]byte) (int, error) {
	return 0, io.EOF
}

func orderedSubset(values, wanted []string) bool {
	index := 0
	for _, value := range values {
		if index < len(wanted) && value == wanted[index] {
			index++
		}
	}
	return index == len(wanted)
}

func TestDeployOverridesUploadedImageWithCurrentConfigWhenNoActive(t *testing.T) {
	digest := "sha256:uploaded"
	snapshot := serviceconfig.Snapshot{
		Source: servicesource.Source{
			Type: servicesource.DockerImageUpload,
			DockerUpload: &servicesource.DockerUpload{
				Repository: "acme/backend", Branch: "main", Workflows: []string{"deploy.yml"},
			},
		},
		Environment: map[string]string{"FIXED": "1"},
		HealthCheck: &serviceconfig.HealthCheck{Port: 8080, Path: "/healthz", TimeoutSeconds: 1},
	}
	normalized, _, _, err := serviceconfig.Canonical(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{
		service: state.ServiceDesired{
			ID: "service", ProjectID: "project", ProjectName: "shop", Name: "api", Enabled: true,
			Snapshot: normalized,
		},
		deployments: make(map[string]state.BeginDeployment),
		failed:      make(map[string]bool),
		latestUploaded: &state.DeploymentRecord{
			ID: "previous-failed", ServiceID: "service", ImageDigest: digest,
			ImageReference: "oci-archive:/images/revision.oci", ImageRevisionID: "revision",
			SourceRevision: "abc123", Status: "failed",
		},
	}
	engine := &fakeEngine{
		containers: make(map[string]containerengine.Container),
		images: map[string]containerengine.Image{
			digest:                          {ID: "uploaded-image", Digest: digest},
			"oci-archive:/images/revision.oci": {ID: "uploaded-image", Digest: digest},
		},
		pullImage: &containerengine.Image{ID: "uploaded-image", Digest: digest},
	}
	publisher := &fakePublisher{}
	ids := []string{"redeploy", "attempt"}
	idIndex := 0
	controller, err := New(Config{
		Store: store, Engine: engine, Publisher: publisher, Growth: allowGrowth, Admission: admission.New(),
		Placement: func(state.ServiceDesired) (Placement, error) {
			return Placement{
				NetworkName: "project-network", Gateway: netip.MustParseAddr("10.80.0.1"),
				DNSSearch: "shop.internal", CgroupParent: "/platformd/workloads/service",
			}, nil
		},
		LogRoot: filepath.Join(t.TempDir(), "logs"), VolumeRoot: filepath.Join(t.TempDir(), "volumes"),
		LogSizeBytes: 1024, LogMaxFiles: 2,
		NewID: func() (string, error) {
			value := ids[idIndex]
			idIndex++
			return value, nil
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(&emptyReader{})}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Deploy(context.Background(), "service", false); err != nil {
		t.Fatal(err)
	}
	if store.service.ActiveDeploymentID != "redeploy" {
		t.Fatalf("active deployment = %q", store.service.ActiveDeploymentID)
	}
	if got := store.deployments["redeploy"]; got.ImageRevisionID != "revision" || got.ImageDigest != digest {
		t.Fatalf("redeployed from previous upload = %+v", got)
	}
}

func TestDeployOverridesFromReusableRevisionWhenNoDeploymentRow(t *testing.T) {
	digest := "sha256:uploaded"
	snapshot := serviceconfig.Snapshot{
		Source: servicesource.Source{
			Type: servicesource.DockerImageUpload,
			DockerUpload: &servicesource.DockerUpload{
				Repository: "acme/backend", Branch: "main", Workflows: []string{"deploy.yml"},
			},
		},
		Environment: map[string]string{"FIXED": "1"},
		HealthCheck: &serviceconfig.HealthCheck{Port: 8080, Path: "/healthz", TimeoutSeconds: 1},
	}
	normalized, _, _, err := serviceconfig.Canonical(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{
		service: state.ServiceDesired{
			ID: "service", ProjectID: "project", ProjectName: "shop", Name: "api", Enabled: true,
			Snapshot: normalized,
		},
		deployments: make(map[string]state.BeginDeployment),
		failed:      make(map[string]bool),
		latestRevision: &state.ImageRevision{
			ID: "revision", ServiceID: "service", ArchivePath: "/images/revision.oci",
			ImageDigest: digest, Status: "retired",
			Identity: state.ImageUploadIdentity{SHA: "abc123"},
		},
	}
	engine := &fakeEngine{
		containers: make(map[string]containerengine.Container),
		images: map[string]containerengine.Image{
			digest:                            {ID: "uploaded-image", Digest: digest},
			"oci-archive:/images/revision.oci": {ID: "uploaded-image", Digest: digest},
		},
		pullImage: &containerengine.Image{ID: "uploaded-image", Digest: digest},
	}
	ids := []string{"redeploy", "attempt"}
	idIndex := 0
	controller, err := New(Config{
		Store: store, Engine: engine, Publisher: &fakePublisher{}, Growth: allowGrowth, Admission: admission.New(),
		Placement: func(state.ServiceDesired) (Placement, error) {
			return Placement{
				NetworkName: "project-network", Gateway: netip.MustParseAddr("10.80.0.1"),
				DNSSearch: "shop.internal", CgroupParent: "/platformd/workloads/service",
			}, nil
		},
		LogRoot: filepath.Join(t.TempDir(), "logs"), VolumeRoot: filepath.Join(t.TempDir(), "volumes"),
		LogSizeBytes: 1024, LogMaxFiles: 2,
		NewID: func() (string, error) {
			value := ids[idIndex]
			idIndex++
			return value, nil
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(&emptyReader{})}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Deploy(context.Background(), "service", false); err != nil {
		t.Fatal(err)
	}
	if got := store.deployments["redeploy"]; got.ImageRevisionID != "revision" || got.SourceRevision != "abc123" {
		t.Fatalf("redeployed from retired revision = %+v", got)
	}
}
