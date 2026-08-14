package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/trafficmetrics"
)

type fakeServiceRuntime struct {
	deployErr   error
	deployForce []bool
	reconciled  []string
	trackRetry  []bool
	failures    []error
	deleted     []string
}

func (runtime *fakeServiceRuntime) DeployService(_ context.Context, _ string, force bool) error {
	runtime.deployForce = append(runtime.deployForce, force)
	return runtime.deployErr
}

func (runtime *fakeServiceRuntime) DeployServiceImage(_ context.Context, _ string, _ state.DeploymentRecord) error {
	runtime.deployForce = append(runtime.deployForce, true)
	return runtime.deployErr
}

func (*fakeServiceRuntime) RestartServiceDeployment(context.Context, string, string) error {
	return nil
}

func (runtime *fakeServiceRuntime) DeleteService(_ context.Context, service state.ServiceDesired) error {
	runtime.deleted = append(runtime.deleted, service.ID)
	return nil
}

func (*fakeServiceRuntime) stopServicePreviews(context.Context, string, string) error { return nil }

func (runtime *fakeServiceRuntime) ReconcileService(_ context.Context, serviceID string) error {
	runtime.reconciled = append(runtime.reconciled, serviceID)
	return nil
}

func (runtime *fakeServiceRuntime) TrackService(_ context.Context, _ string, retry bool) error {
	runtime.trackRetry = append(runtime.trackRetry, retry)
	return nil
}

func (runtime *fakeServiceRuntime) recordServiceFailure(_ string, err error) {
	runtime.failures = append(runtime.failures, err)
}

func TestLiveServiceRepositoryReconcilesMutationsAndPropagatesExplicitRedeployFailure(t *testing.T) {
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(context.Background(), state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeServiceRuntime{}
	traffic := trafficmetrics.NewRegistry()
	repository := liveServiceRepository{store: store, runtime: runtime, traffic: traffic}
	created, err := repository.CreateService(context.Background(), state.CreateService{
		ID: "service", ProjectID: "project", Name: "api", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:latest")},
		AuditEventID: "service-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.reconciled) != 1 || runtime.reconciled[0] != created.ID || len(runtime.deployForce) != 0 {
		t.Fatalf("create runtime calls = reconciled %v, deploy %v", runtime.reconciled, runtime.deployForce)
	}
	if _, err := repository.UpdateService(context.Background(), state.UpdateServiceInput{
		ID: created.ID, ProjectID: created.ProjectID, Enabled: false, Snapshot: created.Snapshot,
		ExpectedUpdatedMillis: created.UpdatedAtMillis,
		AuditEventID:          "update-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if len(runtime.reconciled) != 2 || runtime.reconciled[1] != created.ID || len(runtime.deployForce) != 0 {
		t.Fatalf("update runtime calls = reconciled %v, deploy %v", runtime.reconciled, runtime.deployForce)
	}

	current, err := store.DesiredService(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateService(context.Background(), state.UpdateServiceInput{
		ID: created.ID, ProjectID: created.ProjectID, Enabled: true, Snapshot: current.Snapshot,
		ExpectedUpdatedMillis: current.UpdatedAtMillis,
		AuditEventID:          "enable-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	current, err = store.DesiredService(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	runtime.deployErr = errors.New("registry unavailable")
	_, err = repository.RedeployService(context.Background(), state.RedeployServiceInput{
		ID: created.ID, ProjectID: created.ProjectID, ExpectedUpdatedMillis: current.UpdatedAtMillis,
		AuditEventID: "redeploy-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 5,
	})
	if !errors.Is(err, state.ErrServiceReconcileFailed) {
		t.Fatalf("redeploy error = %v", err)
	}
	if len(runtime.deployForce) != 1 || !runtime.deployForce[0] || len(runtime.trackRetry) != 1 || !runtime.trackRetry[0] {
		t.Fatalf("runtime calls = force %v, retry %v", runtime.deployForce, runtime.trackRetry)
	}
	current, err = store.DesiredService(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	traffic.StartHTTP(created.ID)
	if _, err := repository.DeleteService(context.Background(), state.DeleteServiceInput{
		ID: current.ID, ProjectID: current.ProjectID, ExpectedUpdatedMillis: current.UpdatedAtMillis,
		AuditEventID: "delete-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", DeletedAtMillis: 6,
	}); err != nil {
		t.Fatal(err)
	}
	if len(runtime.deleted) != 1 || runtime.deleted[0] != created.ID {
		t.Fatalf("delete runtime calls = services %v", runtime.deleted)
	}
	if _, exists := traffic.Snapshot()[created.ID]; exists {
		t.Fatal("deleted service traffic counters were retained")
	}
}
