package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/state"
)

// imageControlBackup publishes every currently running uploaded image before
// the SQLite control snapshot that references it. Workloads are not paused;
// a changed active set makes the attempt retry instead of blocking deployment.
type imageControlBackup struct {
	store interface {
		ControlResources(context.Context) (state.ControlResourceIDs, error)
	}
	targets interface {
		ControlTargetID(context.Context) (string, error)
	}
	resources backup.ResourceRunner
	control   backup.ControlRunner
}

func (runner imageControlBackup) RunControl(ctx context.Context) error {
	if runner.store == nil || runner.targets == nil || runner.resources == nil || runner.control == nil {
		return errors.New("image control backup is not configured")
	}
	targetID, err := runner.targets.ControlTargetID(ctx)
	if err != nil {
		return err
	}
	if targetID == "" {
		return backup.ErrControlTargetMissing
	}
	before, err := runner.store.ControlResources(ctx)
	if err != nil {
		return err
	}
	for _, revisionID := range before.Images {
		if _, err := runner.resources.RunResource(ctx, "image", revisionID, targetID, nil, 1); err != nil {
			return fmt.Errorf("backup active image %s: %w", revisionID, err)
		}
	}
	after, err := runner.store.ControlResources(ctx)
	if err != nil {
		return err
	}
	if !slices.Equal(before.Images, after.Images) {
		return errors.New("active uploaded images changed during disaster backup")
	}
	return runner.control.RunControl(ctx)
}
