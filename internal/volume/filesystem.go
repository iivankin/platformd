package volume

import (
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volumestore"
)

type localFilesystem struct {
	root string
}

func (filesystem localFilesystem) Ensure(reference state.PersistentVolumeReference) error {
	_, err := volumestore.EnsureOrdinary(filesystem.root, reference)
	return err
}

func (filesystem localFilesystem) Remove(projectID, volumeID string) error {
	return volumestore.Remove(filesystem.root, projectID, volumeID)
}
