package volume

import (
	"context"

	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volumestore"
)

type managedVolumeRuntime interface {
	RemoveManagedVolume(context.Context, string) error
}

type localFilesystem struct {
	root    string
	runtime managedVolumeRuntime
}

func NewLocalFilesystem(root string, runtime managedVolumeRuntime) Filesystem {
	return localFilesystem{root: root, runtime: runtime}
}

func (filesystem localFilesystem) Ensure(_ context.Context, reference state.PersistentVolumeReference) error {
	_, err := volumestore.EnsureOrdinary(filesystem.root, reference)
	return err
}

func (filesystem localFilesystem) Remove(ctx context.Context, projectID, volumeID string) error {
	if filesystem.runtime != nil {
		if err := filesystem.runtime.RemoveManagedVolume(ctx, volumeID); err != nil {
			// Keep the durable directory if libpod still has a live reference. The
			// filesystem reconciler can remove it safely after runtime cleanup.
			return err
		}
	}
	return volumestore.Remove(filesystem.root, projectID, volumeID)
}
