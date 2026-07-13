package containerlogs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type CleanerConfig struct {
	Root        string
	Retention   time.Duration
	BudgetBytes uint64
}

type Cleaner struct {
	root        string
	retention   time.Duration
	budgetBytes uint64
}

type CleanupResult struct {
	DeletedFiles   int
	DeletedBytes   uint64
	RemainingBytes uint64
	BudgetExceeded bool
}

type cleanupFile struct {
	path     string
	modified time.Time
	size     uint64
	active   bool
	removed  bool
}

func NewCleaner(config CleanerConfig) (*Cleaner, error) {
	if !filepath.IsAbs(config.Root) || filepath.Clean(config.Root) != config.Root ||
		config.Root == string(filepath.Separator) || config.Retention <= 0 {
		return nil, errors.New("container log cleaner root and retention are invalid")
	}
	return &Cleaner{
		root: config.Root, retention: config.Retention, budgetBytes: config.BudgetBytes,
	}, nil
}

// Sweep only removes closed rotated segments or base files whose attempt is no
// longer active. It never renames or truncates a conmon-owned file.
func (cleaner *Cleaner) Sweep(
	ctx context.Context,
	now time.Time,
	activeBasePaths map[string]struct{},
) (CleanupResult, error) {
	if ctx == nil || now.IsZero() {
		return CleanupResult{}, errors.New("container log cleanup context and time are required")
	}
	active := make(map[string]struct{}, len(activeBasePaths))
	for path := range activeBasePaths {
		path = filepath.Clean(path)
		if pathWithinRoot(path, cleaner.root) {
			active[path] = struct{}{}
		}
	}
	files, total, err := cleaner.files(ctx, active)
	if err != nil {
		return CleanupResult{}, err
	}
	result := CleanupResult{RemainingBytes: total}
	cutoff := now.Add(-cleaner.retention)
	for index := range files {
		file := &files[index]
		if file.active || file.modified.After(cutoff) {
			continue
		}
		if err := removeCleanupFile(ctx, file, &result); err != nil {
			return result, err
		}
	}
	if cleaner.budgetBytes > 0 && result.RemainingBytes > cleaner.budgetBytes {
		sort.Slice(files, func(left, right int) bool {
			if !files[left].modified.Equal(files[right].modified) {
				return files[left].modified.Before(files[right].modified)
			}
			return files[left].path < files[right].path
		})
		for index := range files {
			if result.RemainingBytes <= cleaner.budgetBytes {
				break
			}
			file := &files[index]
			if file.active || file.removed {
				continue
			}
			if err := removeCleanupFile(ctx, file, &result); err != nil {
				return result, err
			}
		}
	}
	result.BudgetExceeded = cleaner.budgetBytes > 0 && result.RemainingBytes > cleaner.budgetBytes
	return result, nil
}

func (cleaner *Cleaner) files(
	ctx context.Context,
	active map[string]struct{},
) ([]cleanupFile, uint64, error) {
	files := make([]cleanupFile, 0)
	var total uint64
	err := filepath.WalkDir(cleaner.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) && path == cleaner.root {
				return fs.SkipAll
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		match := segmentName.FindStringSubmatch(entry.Name())
		if len(match) == 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() < 0 {
			return nil
		}
		size := uint64(info.Size())
		if total > math.MaxUint64-size {
			return errors.New("container log size overflows uint64")
		}
		total += size
		basePath := filepath.Join(filepath.Dir(path), match[1]+".log")
		_, baseActive := active[basePath]
		rotation := 0
		if match[2] != "" {
			rotation, _ = strconv.Atoi(match[2])
		}
		files = append(files, cleanupFile{
			path: path, modified: info.ModTime(), size: size,
			active: baseActive && rotation == 0,
		})
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("enumerate container logs: %w", err)
	}
	return files, total, nil
}

func removeCleanupFile(ctx context.Context, file *cleanupFile, result *CleanupResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove closed container log %s: %w", file.path, err)
	}
	file.removed = true
	result.DeletedFiles++
	result.DeletedBytes += file.size
	if file.size >= result.RemainingBytes {
		result.RemainingBytes = 0
	} else {
		result.RemainingBytes -= file.size
	}
	return nil
}

func pathWithinRoot(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." &&
		!filepath.IsAbs(relative) && relative != "" &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
