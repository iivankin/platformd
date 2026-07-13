package daemon

import (
	"context"
	"fmt"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/projectnetwork"
)

// prepareRuntimeHost is deliberately state-independent so every daemon start
// destroys ephemeral runtime state before state.Open can commit a migration.
func prepareRuntimeHost(ctx context.Context, paths layout.Paths, cgroupWorkloadRoot string) error {
	if err := firewall.New().Clear(); err != nil {
		return fmt.Errorf("clear previous platform firewall: %w", err)
	}
	for _, directory := range []string{paths.GeneratedRoot, paths.BackupWorkRoot} {
		if err := resetTransientDirectory(directory); err != nil {
			return err
		}
	}
	if err := projectnetwork.RemoveOwnedBridges(); err != nil {
		return err
	}
	config := containerengine.ProductionConfig(paths, cgroupWorkloadRoot)
	if _, err := containerengine.PrepareStorage(ctx, config); err != nil {
		return fmt.Errorf("prepare private container storage: %w", err)
	}
	return nil
}
