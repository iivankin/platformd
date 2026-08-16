package diskusage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/systemevent"
)

const (
	DefaultRefreshInterval  = 30 * time.Minute
	DefaultEntriesPerSecond = 5_000
	scanPacingBatchSize     = 256
	scanSlowWarningAfter    = 30 * time.Second
)

type Measure func(context.Context) (uint64, error)

type Path struct {
	ID      string
	Path    string
	Parent  string
	Measure Measure
}

type Component struct {
	ID     string
	Bytes  uint64
	Parent string
}

type Snapshot struct {
	Components []Component
	CheckedAt  time.Time
}

// Scanner keeps component usage out of request paths. Run owns all filesystem
// traversal; Components only clones the latest published measurements.
type Scanner struct {
	paths            []Path
	refreshInterval  time.Duration
	entriesPerSecond int
	now              func() time.Time

	mu                 sync.RWMutex
	snapshot           Snapshot
	componentCheckedAt map[string]time.Time
	dirty              map[string]bool
	wake               chan struct{}
}

func NewScanner(paths []Path, refreshInterval time.Duration) (*Scanner, error) {
	if len(paths) == 0 {
		return nil, errors.New("disk usage paths are empty")
	}
	cloned := make([]Path, len(paths))
	components := make([]Component, len(paths))
	checkedAt := make(map[string]time.Time, len(paths))
	dirty := make(map[string]bool, len(paths))
	for index, item := range paths {
		if item.ID == "" || !filepath.IsAbs(item.Path) {
			return nil, errors.New("disk usage path is invalid")
		}
		if _, exists := checkedAt[item.ID]; exists {
			return nil, errors.New("disk usage component IDs must be unique")
		}
		cloned[index] = Path{
			ID: item.ID, Path: filepath.Clean(item.Path), Parent: item.Parent, Measure: item.Measure,
		}
		components[index] = Component{ID: item.ID, Parent: item.Parent}
		checkedAt[item.ID] = time.Time{}
		dirty[item.ID] = true
	}
	ids := make(map[string]struct{}, len(cloned))
	for _, item := range cloned {
		ids[item.ID] = struct{}{}
	}
	for _, item := range cloned {
		if item.Parent == "" {
			continue
		}
		if item.Parent == item.ID {
			return nil, errors.New("disk usage component cannot nest under itself")
		}
		if _, exists := ids[item.Parent]; !exists {
			return nil, errors.New("disk usage nested component parent is unknown")
		}
	}
	if refreshInterval <= 0 {
		refreshInterval = DefaultRefreshInterval
	}
	return &Scanner{
		paths:              cloned,
		refreshInterval:    refreshInterval,
		entriesPerSecond:   DefaultEntriesPerSecond,
		now:                time.Now,
		snapshot:           Snapshot{Components: components},
		componentCheckedAt: checkedAt,
		dirty:              dirty,
		wake:               make(chan struct{}, 1),
	}, nil
}

// Components returns immediately, including before the first background scan.
// A zero CheckedAt means at least one configured component has not been measured.
func (scanner *Scanner) Components(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	scanner.mu.RLock()
	defer scanner.mu.RUnlock()
	return cloneSnapshot(scanner.snapshot), nil
}

// Invalidate schedules a component refresh. Unknown IDs are ignored so callers
// can use this safely from best-effort storage mutation notifications.
func (scanner *Scanner) Invalidate(id string) {
	scanner.mu.Lock()
	_, known := scanner.componentCheckedAt[id]
	if known {
		scanner.dirty[id] = true
	}
	scanner.mu.Unlock()
	if known {
		scanner.signal()
	}
}

func (scanner *Scanner) markDirty(ids []string) {
	if len(ids) == 0 {
		return
	}
	scanner.mu.Lock()
	defer scanner.mu.Unlock()
	for _, id := range ids {
		if _, known := scanner.componentCheckedAt[id]; known {
			scanner.dirty[id] = true
		}
	}
}

// Run scans invalidated components sequentially and periodically rechecks all
// components. The reverse order lets small platform paths publish before the
// container image tree, which is normally the most expensive component.
func (scanner *Scanner) Run(ctx context.Context, onError func(error)) error {
	ticker := time.NewTicker(scanner.refreshInterval)
	defer ticker.Stop()
	for {
		if err := scanner.refreshDirty(ctx, onError); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-scanner.wake:
		case <-ticker.C:
			scanner.invalidateAll()
		}
	}
}

func (scanner *Scanner) refreshDirty(ctx context.Context, onError func(error)) error {
	var failed []string
	for {
		index, ok := scanner.takeDirty()
		if !ok {
			scanner.markDirty(failed)
			return nil
		}
		item := scanner.paths[index]
		startedAt := time.Now()
		slowTimer := time.AfterFunc(scanSlowWarningAfter, func() {
			systemevent.Warning(
				"disk_component_scan_slow",
				systemevent.String("component", item.ID),
				systemevent.Int64("elapsed_ms", scanSlowWarningAfter.Milliseconds()),
			)
		})
		pacer := newScanPacer(scanner.entriesPerSecond, scanner.now)
		var bytes uint64
		var err error
		if item.Measure != nil {
			bytes, err = item.Measure(ctx)
		} else {
			bytes, err = pathBytes(ctx, item.Path, make(map[fileIdentity]struct{}), pacer)
		}
		slowTimer.Stop()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if onError != nil {
				onError(fmt.Errorf("scan disk usage component %s: %w", item.ID, err))
			}
			failed = append(failed, item.ID)
			continue
		}
		if duration := time.Since(startedAt); duration >= scanSlowWarningAfter {
			systemevent.Info(
				"disk_component_scan_finished",
				systemevent.String("component", item.ID),
				systemevent.Uint64("bytes", bytes),
				systemevent.Int64("duration_ms", duration.Milliseconds()),
			)
		}
		scanner.publish(index, bytes, scanner.now())
	}
}

func (scanner *Scanner) takeDirty() (int, bool) {
	scanner.mu.Lock()
	defer scanner.mu.Unlock()
	for index := len(scanner.paths) - 1; index >= 0; index-- {
		id := scanner.paths[index].ID
		if scanner.dirty[id] {
			delete(scanner.dirty, id)
			return index, true
		}
	}
	return 0, false
}

func (scanner *Scanner) publish(index int, bytes uint64, checkedAt time.Time) {
	scanner.mu.Lock()
	defer scanner.mu.Unlock()
	id := scanner.paths[index].ID
	scanner.snapshot.Components[index].Bytes = bytes
	scanner.componentCheckedAt[id] = checkedAt
	scanner.snapshot.CheckedAt = scanner.oldestComponentCheckLocked()
}

func (scanner *Scanner) oldestComponentCheckLocked() time.Time {
	var oldest time.Time
	for _, item := range scanner.paths {
		checkedAt := scanner.componentCheckedAt[item.ID]
		if checkedAt.IsZero() {
			return time.Time{}
		}
		if oldest.IsZero() || checkedAt.Before(oldest) {
			oldest = checkedAt
		}
	}
	return oldest
}

func (scanner *Scanner) invalidateAll() {
	scanner.mu.Lock()
	for _, item := range scanner.paths {
		scanner.dirty[item.ID] = true
	}
	scanner.mu.Unlock()
}

func (scanner *Scanner) signal() {
	select {
	case scanner.wake <- struct{}{}:
	default:
	}
}

type scanPacer struct {
	entriesPerSecond int
	now              func() time.Time
	startedAt        time.Time
	entries          int64
}

func newScanPacer(entriesPerSecond int, now func() time.Time) *scanPacer {
	return &scanPacer{entriesPerSecond: entriesPerSecond, now: now, startedAt: now()}
}

func (pacer *scanPacer) step(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if pacer.entriesPerSecond <= 0 {
		return nil
	}
	pacer.entries++
	if pacer.entries%scanPacingBatchSize != 0 {
		return nil
	}
	expectedElapsed := time.Duration(pacer.entries) * time.Second / time.Duration(pacer.entriesPerSecond)
	delay := expectedElapsed - pacer.now().Sub(pacer.startedAt)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func pathBytes(ctx context.Context, root string, seen map[fileIdentity]struct{}, pacer *scanPacer) (uint64, error) {
	rootInfo, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	rootDevice, identifyFilesystem := deviceOf(rootInfo)
	var total uint64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if err := pacer.step(ctx); err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if path == root || !identifyFilesystem {
				return nil
			}
			info, infoErr := entry.Info()
			if errors.Is(infoErr, fs.ErrNotExist) {
				return fs.SkipDir
			}
			if infoErr != nil {
				return infoErr
			}
			if device, identifiable := deviceOf(info); identifiable && device != rootDevice {
				// Container storage contains live overlay and tmpfs mounts. Their
				// contents are views over already-counted layers, not additional
				// bytes owned by the component.
				return fs.SkipDir
			}
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
