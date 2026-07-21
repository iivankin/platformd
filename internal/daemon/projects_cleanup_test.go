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
