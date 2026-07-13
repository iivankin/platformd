package volumestore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iivankin/platformd/internal/state"
)

type Result struct {
	Created int
	Removed int
}

func Reconcile(
	ctx context.Context,
	root string,
	references []state.PersistentVolumeReference,
) (Result, error) {
	if !safeRoot(root) {
		return Result{}, errors.New("persistent volume root is invalid")
	}
	if err := ensureDirectory(root, 0o700); err != nil {
		return Result{}, fmt.Errorf("prepare persistent volume root: %w", err)
	}

	projects := make(map[string]map[string]state.PersistentVolumeReference)
	for _, reference := range references {
		if !safeComponent(reference.ProjectID) || !safeComponent(reference.VolumeID) {
			return Result{}, fmt.Errorf(
				"persistent volume reference path is invalid: %q/%q",
				reference.ProjectID, reference.VolumeID,
			)
		}
		if err := validateReference(reference); err != nil {
			return Result{}, err
		}
		volumes := projects[reference.ProjectID]
		if volumes == nil {
			volumes = make(map[string]state.PersistentVolumeReference)
			projects[reference.ProjectID] = volumes
		}
		if _, exists := volumes[reference.VolumeID]; exists {
			return Result{}, fmt.Errorf(
				"persistent volume reference is duplicated: %s/%s",
				reference.ProjectID, reference.VolumeID,
			)
		}
		volumes[reference.VolumeID] = reference
	}

	result, err := removeUnreferenced(ctx, root, projects)
	if err != nil {
		return Result{}, err
	}
	for projectID, volumes := range projects {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		projectRoot := filepath.Join(root, projectID)
		if err := ensureDirectory(projectRoot, 0o700); err != nil {
			return Result{}, fmt.Errorf("prepare project volume directory %s: %w", projectID, err)
		}
		for _, reference := range volumes {
			if reference.Kind != state.PersistentVolumeOrdinary {
				continue
			}
			created, err := ensureOrdinary(projectRoot, reference)
			if err != nil {
				return Result{}, err
			}
			if created {
				result.Created++
			}
		}
	}
	return result, nil
}

func EnsureOrdinary(root string, reference state.PersistentVolumeReference) (bool, error) {
	if !safeRoot(root) || !safeComponent(reference.ProjectID) || !safeComponent(reference.VolumeID) {
		return false, errors.New("ordinary volume path is invalid")
	}
	if err := validateReference(reference); err != nil {
		return false, err
	}
	if reference.Kind != state.PersistentVolumeOrdinary {
		return false, errors.New("ordinary volume reference kind is invalid")
	}
	if err := ensureDirectory(root, 0o700); err != nil {
		return false, fmt.Errorf("prepare persistent volume root: %w", err)
	}
	projectRoot := filepath.Join(root, reference.ProjectID)
	if err := ensureDirectory(projectRoot, 0o700); err != nil {
		return false, fmt.Errorf("prepare project volume directory: %w", err)
	}
	return ensureOrdinary(projectRoot, reference)
}

func Remove(root, projectID, volumeID string) error {
	if !safeRoot(root) || !safeComponent(projectID) || !safeComponent(volumeID) {
		return errors.New("volume removal path is invalid")
	}
	if err := requireDirectory(root); err != nil {
		return fmt.Errorf("inspect persistent volume root: %w", err)
	}
	projectRoot := filepath.Join(root, projectID)
	if err := requireDirectory(projectRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect project volume directory: %w", err)
	}
	path := filepath.Join(projectRoot, volumeID)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect volume before removal: %w", err)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove volume: %w", err)
	}
	return syncDirectory(projectRoot)
}

func removeUnreferenced(
	ctx context.Context,
	root string,
	projects map[string]map[string]state.PersistentVolumeReference,
) (Result, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return Result{}, fmt.Errorf("list persistent volume root: %w", err)
	}
	result := Result{}
	for _, projectEntry := range entries {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		projectPath := filepath.Join(root, projectEntry.Name())
		volumes, referencedProject := projects[projectEntry.Name()]
		if !referencedProject {
			if err := os.RemoveAll(projectPath); err != nil {
				return Result{}, fmt.Errorf("remove unreferenced project volumes %q: %w", projectEntry.Name(), err)
			}
			result.Removed++
			continue
		}
		if err := requireDirectory(projectPath); err != nil {
			return Result{}, fmt.Errorf("inspect referenced project volume directory %q: %w", projectEntry.Name(), err)
		}
		volumeEntries, err := os.ReadDir(projectPath)
		if err != nil {
			return Result{}, fmt.Errorf("list project volume directory %q: %w", projectEntry.Name(), err)
		}
		for _, volumeEntry := range volumeEntries {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			volumePath := filepath.Join(projectPath, volumeEntry.Name())
			if _, referenced := volumes[volumeEntry.Name()]; !referenced {
				if err := os.RemoveAll(volumePath); err != nil {
					return Result{}, fmt.Errorf(
						"remove unreferenced volume %q/%q: %w",
						projectEntry.Name(), volumeEntry.Name(), err,
					)
				}
				result.Removed++
				continue
			}
			if err := requireDirectory(volumePath); err != nil {
				return Result{}, fmt.Errorf(
					"inspect referenced volume %q/%q: %w",
					projectEntry.Name(), volumeEntry.Name(), err,
				)
			}
		}
	}
	return result, nil
}

func ensureOrdinary(projectRoot string, reference state.PersistentVolumeReference) (bool, error) {
	path := filepath.Join(projectRoot, reference.VolumeID)
	if err := requireDirectory(path); err == nil {
		// Ownership is intentionally only applied during creation. Users may
		// later repair populated volumes through an explicit console action.
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf(
			"inspect ordinary volume %s/%s: %w",
			reference.ProjectID, reference.VolumeID, err,
		)
	}

	temporary, err := os.MkdirTemp(projectRoot, ".platformd-volume-")
	if err != nil {
		return false, fmt.Errorf("create temporary ordinary volume: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := os.Chmod(temporary, 0o700); err != nil {
		return false, fmt.Errorf("set ordinary volume mode: %w", err)
	}
	if err := os.Chown(temporary, reference.OwnerUID, reference.OwnerGID); err != nil {
		return false, fmt.Errorf("set ordinary volume owner: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return false, fmt.Errorf("publish ordinary volume: %w", err)
	}
	committed = true
	if err := syncDirectory(projectRoot); err != nil {
		return false, err
	}
	return true, nil
}

func validateReference(reference state.PersistentVolumeReference) error {
	switch reference.Kind {
	case state.PersistentVolumeOrdinary:
		if !validOwner(reference.OwnerUID) || !validOwner(reference.OwnerGID) {
			return errors.New("ordinary volume owner is invalid")
		}
	case state.PersistentVolumePostgres, state.PersistentVolumeRedis:
	default:
		return fmt.Errorf("persistent volume kind %q is invalid", reference.Kind)
	}
	return nil
}

func validOwner(value int) bool {
	return value >= 0 && int64(value) <= int64(1<<32-2)
}

func ensureDirectory(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	return requireDirectory(path)
}

func requireDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("path is not a real directory")
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open volume parent for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync volume parent: %w", err)
	}
	return nil
}

func safeRoot(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && value != string(filepath.Separator)
}

func safeComponent(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value &&
		!strings.ContainsAny(value, "/\\\x00")
}
