//go:build !platformd_worker

package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

type projectVolumeRuntimeStub struct {
	failID  string
	removed []string
}

func (runtime *projectVolumeRuntimeStub) RemoveManagedVolume(_ context.Context, volumeID string) error {
	runtime.removed = append(runtime.removed, volumeID)
	if volumeID == runtime.failID {
		return errors.New("volume busy")
	}
	return nil
}

type hostStatusStub struct {
	status  string
	message string
}

func (stub hostStatusStub) ServiceStatus(string, string, bool) (string, string) {
	return stub.status, stub.message
}

func TestServiceCanvasStatusUsesHostReports(t *testing.T) {
	t.Parallel()

	remote := &state.CanvasResource{ID: "api", HostID: "host-edge", Enabled: true}
	status, message := liveProjectRepository{}.serviceCanvasStatus(remote)
	if status != "pending" || message != "Child server is offline" {
		t.Fatalf("nil hosts = %q/%q", status, message)
	}

	status, message = (liveProjectRepository{
		hosts: hostStatusStub{status: "running"},
	}).serviceCanvasStatus(remote)
	if status != "running" || message != "" {
		t.Fatalf("reported running = %q/%q", status, message)
	}

	status, message = (liveProjectRepository{
		hosts: hostStatusStub{status: "pending", message: "Waiting for child status"},
	}).serviceCanvasStatus(remote)
	if status != "pending" || message != "Waiting for child status" {
		t.Fatalf("awaiting report = %q/%q", status, message)
	}
}

func TestApplyCanvasServiceRuntimeStatusPreservesFailedAndDegraded(t *testing.T) {
	t.Parallel()

	remote := state.CanvasResource{
		Enabled: true, HostID: "host-edge", Status: "pending", ActiveDeployment: "deploy-1",
	}
	applyCanvasServiceRuntimeStatus(&remote, "running", "")
	if remote.Status != "running" || remote.StatusMessage != "" {
		t.Fatalf("remote running overlay = %q/%q", remote.Status, remote.StatusMessage)
	}

	offline := state.CanvasResource{
		Enabled: true, HostID: "host-edge", Status: "pending", ActiveDeployment: "deploy-1",
	}
	applyCanvasServiceRuntimeStatus(&offline, "pending", "Child server is offline")
	if offline.Status != "pending" || offline.StatusMessage != "Child server is offline" {
		t.Fatalf("remote offline overlay = %q/%q", offline.Status, offline.StatusMessage)
	}

	failed := state.CanvasResource{
		Enabled: true, HostID: "host-edge", Status: "failed", StatusMessage: "readiness failed",
	}
	applyCanvasServiceRuntimeStatus(&failed, "pending", "Child server is offline")
	if failed.Status != "failed" || failed.StatusMessage != "readiness failed" {
		t.Fatalf("remote failed service = %q/%q", failed.Status, failed.StatusMessage)
	}

	degraded := state.CanvasResource{
		Enabled: true, HostID: "host-edge", Status: "degraded", StatusMessage: "latest deploy failed",
	}
	applyCanvasServiceRuntimeStatus(&degraded, "running", "")
	if degraded.Status != "degraded" || degraded.StatusMessage != "latest deploy failed" {
		t.Fatalf("remote degraded service = %q/%q", degraded.Status, degraded.StatusMessage)
	}

	local := state.CanvasResource{Enabled: true, Status: "pending"}
	applyCanvasServiceRuntimeStatus(&local, "running", "")
	if local.Status != "running" {
		t.Fatalf("local service = %q", local.Status)
	}
}

func TestRemoveProjectManagedVolumesReportsFailureAfterTryingEveryVolume(t *testing.T) {
	t.Parallel()
	runtime := &projectVolumeRuntimeStub{failID: "postgres-volume"}
	plan := state.ProjectDeletionPlan{
		Volumes:  []state.Volume{{ID: "service-volume"}},
		Postgres: []state.ManagedPostgres{{VolumeID: "postgres-volume"}},
		Redis:    []state.ManagedRedis{{VolumeID: "redis-volume"}},
	}

	err := removeProjectManagedVolumes(context.Background(), runtime, plan)
	if err == nil || !reflect.DeepEqual(runtime.removed, []string{
		"service-volume", "postgres-volume", "redis-volume",
	}) {
		t.Fatalf("removed = %v, error = %v", runtime.removed, err)
	}
}
