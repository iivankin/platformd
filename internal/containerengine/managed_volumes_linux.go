//go:build linux && amd64 && cgo

package containerengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containers/podman/v5/libpod"
	"github.com/containers/podman/v5/libpod/define"
	"github.com/containers/podman/v5/pkg/specgen"
	buildspec "github.com/opencontainers/runtime-spec/specs-go"
)

const (
	managedVolumeNamePrefix = "platformd-"
	managedVolumeOwnerLabel = "io.platformd.owner"
	managedVolumeIDLabel    = "io.platformd.volume-id"
)

func (e *Engine) runtimeManagedVolumes(
	ctx context.Context,
	volumes []ManagedVolumeMount,
	mounts []buildspec.Mount,
) ([]*specgen.NamedVolume, error) {
	destinations := make(map[string]struct{}, len(mounts)+len(volumes))
	for _, mount := range mounts {
		destinations[mount.Destination] = struct{}{}
	}
	result := make([]*specgen.NamedVolume, 0, len(volumes))
	for _, volume := range volumes {
		if volume.ID == "" {
			return nil, errors.New("managed volume ID is empty")
		}
		if err := validateAbsolutePath("managed volume destination", volume.Destination); err != nil {
			return nil, err
		}
		if volume.Destination == "/" {
			return nil, errors.New("managed volume cannot replace container root")
		}
		if _, exists := destinations[volume.Destination]; exists {
			return nil, fmt.Errorf("duplicate mount destination %s", volume.Destination)
		}
		destinations[volume.Destination] = struct{}{}
		resolved, err := e.resolveManagedVolumeSource(volume.Source)
		if err != nil {
			return nil, err
		}
		name := managedVolumeNamePrefix + volume.ID
		if err := e.ensureManagedVolume(ctx, name, volume.ID, resolved, volume.Initialized); err != nil {
			return nil, err
		}
		options := []string{"rw"}
		if volume.ReadOnly {
			options[0] = "ro"
		}
		if volume.Initialized {
			options = append(options, "nocopy")
		}
		result = append(result, &specgen.NamedVolume{
			Name: name, Dest: volume.Destination, Options: options,
		})
	}
	return result, nil
}

func (e *Engine) ensureManagedVolume(
	ctx context.Context,
	name string,
	volumeID string,
	source string,
	initialized bool,
) error {
	exists, err := e.runtime.HasVolume(name)
	if err != nil {
		return fmt.Errorf("inspect managed volume %s: %w", volumeID, err)
	}
	if exists {
		stored, err := e.runtime.GetVolume(name)
		if err != nil {
			return fmt.Errorf("load managed volume %s: %w", volumeID, err)
		}
		labels, options := stored.Labels(), stored.Options()
		if labels[managedVolumeOwnerLabel] != "durable-volume" ||
			labels[managedVolumeIDLabel] != volumeID ||
			options["type"] != define.TypeBind || options["device"] != source {
			return fmt.Errorf("managed volume %s has conflicting runtime state", volumeID)
		}
		return nil
	}
	options := map[string]string{"type": define.TypeBind, "device": source}
	if initialized {
		options["nocopy"] = ""
	}
	createOptions := []libpod.VolumeCreateOption{
		libpod.WithVolumeName(name),
		libpod.WithVolumeLabels(map[string]string{
			managedVolumeOwnerLabel: "durable-volume",
			managedVolumeIDLabel:    volumeID,
		}),
		libpod.WithVolumeOptions(options),
		libpod.WithVolumeDisableQuota(),
	}
	if initialized {
		// Runtime state lives under /run and can disappear independently of
		// durable volume data. The SQLite marker prevents an empty restored
		// volume from being populated or chowned again after that happens.
		createOptions = append(createOptions, libpod.WithVolumeNoChown())
	}
	if _, err := e.runtime.NewVolume(ctx, createOptions...); err != nil {
		return fmt.Errorf("create managed volume %s: %w", volumeID, err)
	}
	return nil
}

func (e *Engine) resolveManagedVolumeSource(source string) (string, error) {
	if err := validateAbsolutePath("managed volume source", source); err != nil {
		return "", err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return "", fmt.Errorf("inspect managed volume source %s: %w", source, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("managed volume source %s must be a directory, not a symlink", source)
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return "", fmt.Errorf("resolve managed volume source %s: %w", source, err)
	}
	for _, root := range e.config.AllowedMountRoots {
		if pathWithin(resolved, root) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("managed volume source %s is outside managed roots", source)
}

func (e *Engine) RemoveManagedVolume(ctx context.Context, volumeID string) error {
	if volumeID == "" {
		return errors.New("managed volume ID is empty")
	}
	name := managedVolumeNamePrefix + volumeID
	stored, err := e.runtime.GetVolume(name)
	if errors.Is(err, define.ErrNoSuchVolume) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load managed volume %s: %w", volumeID, err)
	}
	labels := stored.Labels()
	if labels[managedVolumeOwnerLabel] != "durable-volume" || labels[managedVolumeIDLabel] != volumeID {
		return fmt.Errorf("refusing to remove unmanaged runtime volume %s", name)
	}
	if err := e.runtime.RemoveVolume(ctx, stored, false, nil); err != nil {
		return fmt.Errorf("remove managed volume %s: %w", volumeID, err)
	}
	return nil
}
