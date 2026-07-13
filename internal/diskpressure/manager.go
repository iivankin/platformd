package diskpressure

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	CheckInterval       = 5 * time.Second
	minimumReserveBytes = uint64(1 << 30)
	reserveFraction     = uint64(50)
	percentageScale     = uint64(10_000)
)

type Level string

const (
	Normal    Level = "normal"
	Low       Level = "low"
	Critical  Level = "critical"
	Emergency Level = "emergency"
)

var ErrGrowthDenied = errors.New("disk-growing operation denied by disk pressure")

type Usage struct {
	TotalBytes       uint64
	AvailableBytes   uint64
	TotalInodes      uint64
	AvailableInodes  uint64
	ByteBasisPoints  uint64
	InodeBasisPoints uint64
}

type Snapshot struct {
	Level          Level
	Usage          Usage
	ReservePresent bool
	CheckedAt      time.Time
}

type Collector interface {
	Collect(string) (Usage, error)
}

type Reserve interface {
	Ensure(string, int64) error
	Remove(string) error
	Present(string, int64) (bool, error)
}

type Freezer interface {
	SetFrozen(context.Context, bool) error
}

type TransitionSink interface {
	DiskPressureTransition(context.Context, Level, Level, Usage, time.Time) error
}

type Cleanup func(context.Context) error

type Config struct {
	DataRoot    string
	ReservePath string
	Collector   Collector
	Reserve     Reserve
	Freezer     Freezer
	Transitions TransitionSink
	Cleanup     Cleanup
	Now         func() time.Time
}

type Manager struct {
	dataRoot    string
	reservePath string
	collector   Collector
	reserve     Reserve
	freezer     Freezer
	transitions TransitionSink
	cleanup     Cleanup
	now         func() time.Time

	checkMu sync.Mutex
	stateMu sync.RWMutex
	state   Snapshot
	ready   bool
}

func New(config Config) (*Manager, error) {
	if !canonicalAbsolute(config.DataRoot) || !canonicalAbsolute(config.ReservePath) || !pathWithin(config.ReservePath, config.DataRoot) {
		return nil, errors.New("disk pressure paths must be canonical absolute paths on the data tree")
	}
	if config.Collector == nil || config.Reserve == nil || config.Freezer == nil {
		return nil, errors.New("disk pressure dependencies are incomplete")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		dataRoot: config.DataRoot, reservePath: config.ReservePath,
		collector: config.Collector, reserve: config.Reserve, freezer: config.Freezer,
		transitions: config.Transitions, cleanup: config.Cleanup, now: now,
	}, nil
}

func (manager *Manager) Check(ctx context.Context) (Snapshot, error) {
	manager.checkMu.Lock()
	defer manager.checkMu.Unlock()
	usage, err := manager.collector.Collect(manager.dataRoot)
	if err != nil {
		return Snapshot{}, err
	}
	if err := validateUsage(usage); err != nil {
		return Snapshot{}, err
	}
	usage.ByteBasisPoints = usedBasisPoints(usage.TotalBytes-usage.AvailableBytes, usage.TotalBytes)
	usage.InodeBasisPoints = usedBasisPoints(usage.TotalInodes-usage.AvailableInodes, usage.TotalInodes)

	manager.stateMu.RLock()
	previous := manager.state
	ready := manager.ready
	manager.stateMu.RUnlock()
	next := entryLevel(usage)
	if ready {
		next = nextLevel(previous.Level, usage)
	}
	if next == Emergency && (!ready || previous.Level != Emergency) {
		if err := manager.reserve.Remove(manager.reservePath); err != nil {
			return Snapshot{}, fmt.Errorf("release emergency disk reserve: %w", err)
		}
		if err := manager.freezer.SetFrozen(ctx, true); err != nil {
			return Snapshot{}, fmt.Errorf("freeze workload cgroups: %w", err)
		}
	}
	if ready && previous.Level == Emergency && next != Emergency {
		if err := manager.freezer.SetFrozen(ctx, false); err != nil {
			return Snapshot{}, fmt.Errorf("unfreeze workload cgroups: %w", err)
		}
	}
	reserveSize, err := reserveSize(usage.TotalBytes)
	if err != nil {
		return Snapshot{}, err
	}
	if next == Normal && projectedBasisPoints(usage, uint64(reserveSize)) < 9_000 {
		if err := manager.reserve.Ensure(manager.reservePath, reserveSize); err != nil {
			return Snapshot{}, fmt.Errorf("ensure disk reserve: %w", err)
		}
	}
	present, err := manager.reserve.Present(manager.reservePath, reserveSize)
	if err != nil {
		return Snapshot{}, fmt.Errorf("inspect disk reserve: %w", err)
	}
	checkedAt := manager.now()
	snapshot := Snapshot{Level: next, Usage: usage, ReservePresent: present, CheckedAt: checkedAt}
	if ready && previous.Level != next && manager.transitions != nil {
		if err := manager.transitions.DiskPressureTransition(ctx, previous.Level, next, usage, checkedAt); err != nil {
			return Snapshot{}, err
		}
	}
	manager.stateMu.Lock()
	manager.state = snapshot
	manager.ready = true
	manager.stateMu.Unlock()

	var followup []error
	if next != Normal && manager.cleanup != nil {
		followup = append(followup, manager.cleanup(ctx))
	}
	return snapshot, errors.Join(followup...)
}

func (manager *Manager) Run(ctx context.Context, onError func(error)) error {
	if _, ready := manager.Snapshot(); !ready {
		if _, err := manager.Check(ctx); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := manager.Check(ctx); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}

func (manager *Manager) Snapshot() (Snapshot, bool) {
	manager.stateMu.RLock()
	defer manager.stateMu.RUnlock()
	return manager.state, manager.ready
}

func (manager *Manager) PermitGrowth(ctx context.Context) error {
	snapshot, err := manager.Check(ctx)
	if err != nil {
		return err
	}
	if snapshot.Level == Critical || snapshot.Level == Emergency {
		return fmt.Errorf("%w: level %s", ErrGrowthDenied, snapshot.Level)
	}
	return nil
}

func entryLevel(usage Usage) Level {
	worst := max(usage.ByteBasisPoints, usage.InodeBasisPoints)
	switch {
	case worst >= 9_700:
		return Emergency
	case worst >= 9_500:
		return Critical
	case worst >= 9_000:
		return Low
	default:
		return Normal
	}
}

func nextLevel(previous Level, usage Usage) Level {
	entry := entryLevel(usage)
	if levelRank(entry) > levelRank(previous) {
		return entry
	}
	bytes := usage.ByteBasisPoints
	inodes := usage.InodeBasisPoints
	switch previous {
	case Emergency:
		if bytes < 9_500 && inodes < 9_500 {
			return Critical
		}
	case Critical:
		if bytes < 9_300 && inodes < 9_300 {
			return Low
		}
	case Low:
		if bytes < 8_800 && inodes < 8_800 {
			return Normal
		}
	case Normal:
		return entry
	}
	return previous
}

func levelRank(level Level) int {
	switch level {
	case Normal:
		return 0
	case Low:
		return 1
	case Critical:
		return 2
	case Emergency:
		return 3
	default:
		return -1
	}
}

func validateUsage(usage Usage) error {
	if usage.TotalBytes == 0 || usage.AvailableBytes > usage.TotalBytes || usage.AvailableInodes > usage.TotalInodes {
		return errors.New("disk pressure usage is invalid")
	}
	return nil
}

func usedBasisPoints(used, total uint64) uint64 {
	if total == 0 {
		return 0
	}
	high, low := bits.Mul64(used, percentageScale)
	quotient, _ := bits.Div64(high, low, total)
	return min(quotient, percentageScale)
}

func reserveSize(total uint64) (int64, error) {
	size := max(minimumReserveBytes, total/reserveFraction)
	if size > uint64(^uint64(0)>>1) {
		return 0, errors.New("disk reserve size exceeds int64")
	}
	return int64(size), nil
}

func projectedBasisPoints(usage Usage, reserve uint64) uint64 {
	used := usage.TotalBytes - usage.AvailableBytes
	if reserve > usage.AvailableBytes {
		return percentageScale
	}
	return usedBasisPoints(used+reserve, usage.TotalBytes)
}

func canonicalAbsolute(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && value != string(filepath.Separator)
}

func pathWithin(value, root string) bool {
	relative, err := filepath.Rel(root, value)
	return err == nil && relative != "." && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
