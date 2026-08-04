package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/iivankin/platformd/internal/diskusage"
	"github.com/iivankin/platformd/internal/state"
)

type resourceMetricDiskStore interface {
	ResourceMetricDiskTargets(context.Context) ([]state.ResourceMetricDiskTarget, error)
}

type resourceMetricDiskPaths struct {
	store resourceMetricDiskStore
	root  string
}

func (source resourceMetricDiskPaths) ResourceDiskPaths(ctx context.Context) ([]diskusage.ResourcePath, error) {
	targets, err := source.store.ResourceMetricDiskTargets(ctx)
	if err != nil {
		return nil, err
	}
	paths := make([]diskusage.ResourcePath, 0, len(targets))
	for _, target := range targets {
		if !safeMetricPathComponent(target.ProjectID) {
			return nil, errors.New("resource metric project path is invalid")
		}
		resource := diskusage.ResourcePath{
			Kind: target.Kind, ResourceID: target.ResourceID, ProjectID: target.ProjectID,
			Paths: make([]string, 0, len(target.VolumeIDs)),
		}
		for _, volumeID := range target.VolumeIDs {
			if !safeMetricPathComponent(volumeID) {
				return nil, errors.New("resource metric volume path is invalid")
			}
			resource.Paths = append(resource.Paths, filepath.Join(source.root, target.ProjectID, volumeID))
		}
		paths = append(paths, resource)
	}
	return paths, nil
}

func safeMetricPathComponent(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value &&
		!strings.ContainsAny(value, "/\\\x00")
}
