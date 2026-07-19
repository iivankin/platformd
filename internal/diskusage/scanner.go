package diskusage

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sync"
	"time"
)

const DefaultCacheTTL = time.Minute

type Path struct {
	ID   string
	Path string
}

type Component struct {
	ID    string
	Bytes uint64
}

type Snapshot struct {
	Components []Component
	CheckedAt  time.Time
}

type Scanner struct {
	paths    []Path
	cacheTTL time.Duration
	now      func() time.Time

	mu       sync.Mutex
	snapshot Snapshot
}

func NewScanner(paths []Path, cacheTTL time.Duration) (*Scanner, error) {
	if len(paths) == 0 {
		return nil, errors.New("disk usage paths are empty")
	}
	cloned := make([]Path, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for index, item := range paths {
		if item.ID == "" || !filepath.IsAbs(item.Path) {
			return nil, errors.New("disk usage path is invalid")
		}
		if _, exists := seen[item.ID]; exists {
			return nil, errors.New("disk usage component IDs must be unique")
		}
		seen[item.ID] = struct{}{}
		cloned[index] = Path{ID: item.ID, Path: filepath.Clean(item.Path)}
	}
	if cacheTTL <= 0 {
		cacheTTL = DefaultCacheTTL
	}
	return &Scanner{paths: cloned, cacheTTL: cacheTTL, now: time.Now}, nil
}

func (scanner *Scanner) Components(ctx context.Context) (Snapshot, error) {
	scanner.mu.Lock()
	defer scanner.mu.Unlock()
	now := scanner.now()
	if !scanner.snapshot.CheckedAt.IsZero() && now.Sub(scanner.snapshot.CheckedAt) < scanner.cacheTTL {
		return cloneSnapshot(scanner.snapshot), nil
	}

	components := make([]Component, 0, len(scanner.paths))
	seenFiles := make(map[fileIdentity]struct{})
	for _, item := range scanner.paths {
		bytes, err := pathBytes(ctx, item.Path, seenFiles)
		if err != nil {
			return Snapshot{}, err
		}
		components = append(components, Component{ID: item.ID, Bytes: bytes})
	}
	scanner.snapshot = Snapshot{Components: components, CheckedAt: now}
	return cloneSnapshot(scanner.snapshot), nil
}

func pathBytes(ctx context.Context, root string, seen map[fileIdentity]struct{}) (uint64, error) {
	var total uint64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		identity, identifiable := identityOf(info)
		if identifiable {
			if _, exists := seen[identity]; exists {
				return nil
			}
			seen[identity] = struct{}{}
		}
		total += allocatedBytes(info)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return total, err
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	return Snapshot{Components: append([]Component(nil), snapshot.Components...), CheckedAt: snapshot.CheckedAt}
}
