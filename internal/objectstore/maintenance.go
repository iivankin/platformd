package objectstore

import (
	"context"
	"sync"
	"time"
)

type dataPlaneMaintenance interface {
	BeginDataPlaneBackup(context.Context, string) error
	EndDataPlaneBackup(context.Context, string) error
	BeginDataPlaneRestore(context.Context, string) error
	EndDataPlaneRestore(context.Context, string) error
}

func (application *Application) beginDataPlaneBackup(ctx context.Context, storeID string) (func() error, error) {
	maintenance, ok := application.storage.(dataPlaneMaintenance)
	if !ok {
		return func() error { return nil }, nil
	}
	if err := maintenance.BeginDataPlaneBackup(ctx, storeID); err != nil {
		return nil, err
	}
	return onceMaintenanceRelease(func(ctx context.Context) error {
		return maintenance.EndDataPlaneBackup(ctx, storeID)
	}), nil
}

func (application *Application) beginDataPlaneRestore(ctx context.Context, storeID string) (func() error, error) {
	maintenance, ok := application.storage.(dataPlaneMaintenance)
	if !ok {
		return func() error { return nil }, nil
	}
	if err := maintenance.BeginDataPlaneRestore(ctx, storeID); err != nil {
		return nil, err
	}
	return onceMaintenanceRelease(func(ctx context.Context) error {
		return maintenance.EndDataPlaneRestore(ctx, storeID)
	}), nil
}

func onceMaintenanceRelease(release func(context.Context) error) func() error {
	var once sync.Once
	var result error
	return func() error {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result = release(ctx)
		})
		return result
	}
}
