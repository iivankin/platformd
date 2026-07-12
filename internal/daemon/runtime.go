package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/layout"
)

type runtimeStack struct {
	engine   *containerengine.Engine
	firewall *firewall.Manager
}

func startRuntime(ctx context.Context, paths layout.Paths, cgroupWorkloadRoot string) (*runtimeStack, error) {
	manager := firewall.New()
	if err := manager.Clear(); err != nil {
		return nil, fmt.Errorf("clear previous platform firewall: %w", err)
	}
	if err := firewall.EnableIPv4Forwarding(); err != nil {
		return nil, err
	}
	for _, directory := range []string{paths.GeneratedRoot, paths.BackupWorkRoot} {
		if err := resetTransientDirectory(directory); err != nil {
			return nil, err
		}
	}

	config := containerengine.ProductionConfig(paths, cgroupWorkloadRoot)
	if _, err := containerengine.PrepareStorage(ctx, config); err != nil {
		return nil, fmt.Errorf("prepare private container storage: %w", err)
	}
	engine, err := containerengine.Open(ctx, config)
	if err != nil {
		return nil, errors.Join(err, manager.Clear())
	}
	return &runtimeStack{engine: engine, firewall: manager}, nil
}

func (stack *runtimeStack) Close() error {
	return errors.Join(stack.engine.Close(), stack.firewall.Clear())
}

func resetTransientDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
		return fmt.Errorf("unsafe transient directory %q", path)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("clear transient directory %s: %w", path, err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create transient directory %s: %w", path, err)
	}
	return nil
}
