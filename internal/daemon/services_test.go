package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
)

type fakeServiceRuntime struct {
	deployErr       error
	deployForce     []bool
	deployRevisions []string
	reconciled      []string
	trackRetry      []bool
	failures        []error
	deleted         []string
	logsDeleted     []string
}

func (runtime *fakeServiceRuntime) DeployService(_ context.Context, _ string, force bool) error {
	runtime.deployForce = append(runtime.deployForce, force)
	return runtime.deployErr
}

func (runtime *fakeServiceRuntime) DeployServiceRevision(_ context.Context, _, revision string, force bool) error {
	runtime.deployRevisions = append(runtime.deployRevisions, revision)
	runtime.deployForce = append(runtime.deployForce, force)
	return runtime.deployErr
}

func (*fakeServiceRuntime) RestartServiceDeployment(context.Context, string, string) error {
	return nil
}

func (*fakeServiceRuntime) DeleteServiceDeploymentLogs(string, string) error { return nil }

func (runtime *fakeServiceRuntime) DeleteService(_ context.Context, service state.ServiceDesired) error {
	runtime.deleted = append(runtime.deleted, service.ID)
	return nil
}

func (runtime *fakeServiceRuntime) DeleteServiceLogs(serviceID string) error {
	runtime.logsDeleted = append(runtime.logsDeleted, serviceID)
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
	repository := liveServiceRepository{store: store, runtime: runtime}
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
	if _, err := repository.DeleteService(context.Background(), state.DeleteServiceInput{
		ID: current.ID, ProjectID: current.ProjectID, ExpectedUpdatedMillis: current.UpdatedAtMillis,
		AuditEventID: "delete-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", DeletedAtMillis: 6,
	}); err != nil {
		t.Fatal(err)
	}
	if len(runtime.deleted) != 1 || runtime.deleted[0] != created.ID || len(runtime.logsDeleted) != 1 {
		t.Fatalf("delete runtime calls = services %v logs %v", runtime.deleted, runtime.logsDeleted)
	}
}

func TestLiveServiceRepositoryDeploysGitHubVersionWithTransientRevision(t *testing.T) {
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
	snapshot := serviceconfig.Snapshot{Source: servicesource.Source{
		Type: servicesource.GitHubImage,
		GitHub: &servicesource.GitHub{
			RepositoryID: 42, Repository: "owner/repository", Branch: "main",
			DockerfilePath: "Dockerfile", ContextPath: ".", WaitForCI: true,
		},
	}}
	service, err := store.CreateService(context.Background(), state.CreateService{
		ID: "service", ProjectID: "project", Name: "api", Enabled: true,
		Snapshot: snapshot, AuditEventID: "service-audit", ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, snapshotJSON, hash, err := serviceconfig.Canonical(service.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 40)
	if err := store.BeginDeployment(context.Background(), state.BeginDeployment{
		ID: "skipped", ServiceID: service.ID, SourceRevision: revision,
		ConfigHash: hash, SnapshotJSON: snapshotJSON, Status: "skipped",
		CreatedAtMillis: 3, FinishedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeServiceRuntime{}
	updated, err := (liveServiceRepository{store: store, runtime: runtime}).DeployServiceVersion(
		context.Background(), state.DeployServiceVersionInput{
			ID: service.ID, ProjectID: service.ProjectID, DeploymentID: "skipped",
			ExpectedUpdatedMillis: service.UpdatedAtMillis, AuditEventID: "deploy-audit",
			ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 5,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.deployRevisions) != 1 || runtime.deployRevisions[0] != revision ||
		len(runtime.deployForce) != 1 || !runtime.deployForce[0] {
		t.Fatalf("GitHub deployment calls = revisions %v force %v", runtime.deployRevisions, runtime.deployForce)
	}
	if updated.Snapshot.Source.GitHub == nil || updated.Snapshot.Source.GitHub.Revision != "" ||
		!updated.Snapshot.Source.GitHub.WaitForCI {
		t.Fatalf("updated GitHub source = %+v", updated.Snapshot.Source.GitHub)
	}
}
