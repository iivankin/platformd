package diskpressure

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

const testFilesystemBytes = uint64(100 << 30)

type collectorStub struct {
	usage Usage
}

func (collector *collectorStub) Collect(string) (Usage, error) {
	return collector.usage, nil
}

func usageAt(byteBasisPoints, inodeBasisPoints uint64) Usage {
	return Usage{
		TotalBytes:      testFilesystemBytes,
		AvailableBytes:  testFilesystemBytes - testFilesystemBytes*byteBasisPoints/percentageScale,
		TotalInodes:     10_000,
		AvailableInodes: 10_000 - inodeBasisPoints,
	}
}

type reserveStub struct {
	present bool
	ensures int
	removes int
}

func (reserve *reserveStub) Ensure(_ string, size int64) error {
	if size != 2<<30 {
		return fmt.Errorf("reserve size = %d", size)
	}
	if !reserve.present {
		reserve.present = true
		reserve.ensures++
	}
	return nil
}

func (reserve *reserveStub) Remove(string) error {
	reserve.present = false
	reserve.removes++
	return nil
}

func (reserve *reserveStub) Present(_ string, size int64) (bool, error) {
	if size != 2<<30 {
		return false, fmt.Errorf("reserve size = %d", size)
	}
	return reserve.present, nil
}

type freezerStub struct {
	states []bool
}

func (freezer *freezerStub) SetFrozen(_ context.Context, frozen bool) error {
	freezer.states = append(freezer.states, frozen)
	return nil
}

type transitionStub struct {
	values []string
}

func (sink *transitionStub) DiskPressureTransition(_ context.Context, from, to Level, _ Usage, _ time.Time) error {
	sink.values = append(sink.values, string(from)+"->"+string(to))
	return nil
}

func TestManagerAppliesEntryHysteresisReserveAndFreeze(t *testing.T) {
	t.Parallel()

	collector := &collectorStub{usage: usageAt(8_700, 1_000)}
	reserve := &reserveStub{}
	freezer := &freezerStub{}
	transitions := &transitionStub{}
	cleanupCount := 0
	manager, err := New(Config{
		DataRoot: "/var/lib/platformd", ReservePath: "/var/lib/platformd/.reserve",
		Collector: collector, Reserve: reserve, Freezer: freezer, Transitions: transitions,
		Cleanup: func(context.Context) error { cleanupCount++; return nil },
		Now:     func() time.Time { return time.Unix(1, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	check := func(bytes, inodes uint64, want Level) {
		t.Helper()
		collector.usage = usageAt(bytes, inodes)
		snapshot, err := manager.Check(context.Background())
		if err != nil || snapshot.Level != want {
			t.Fatalf("usage %d/%d => %+v, %v; want %s", bytes, inodes, snapshot, err, want)
		}
	}
	check(8_700, 1_000, Normal)
	if !reserve.present || reserve.ensures != 1 || len(transitions.values) != 0 {
		t.Fatalf("initial reserve/transitions = %+v/%v", reserve, transitions.values)
	}
	check(9_000, 1_000, Low)
	check(8_900, 1_000, Low)
	check(8_700, 1_000, Normal)
	check(9_600, 1_000, Critical)
	check(9_400, 1_000, Critical)
	check(9_200, 1_000, Low)
	check(9_800, 1_000, Emergency)
	if reserve.present || reserve.removes != 1 || len(freezer.states) != 1 || !freezer.states[0] {
		t.Fatalf("emergency reserve/freezer = %+v/%v", reserve, freezer.states)
	}
	check(9_400, 1_000, Critical)
	if len(freezer.states) != 2 || freezer.states[1] {
		t.Fatalf("emergency exit freezer = %v", freezer.states)
	}
	if cleanupCount != 7 {
		t.Fatalf("cleanup count = %d", cleanupCount)
	}
	wantTransitions := []string{
		"normal->low", "low->normal", "normal->critical", "critical->low", "low->emergency", "emergency->critical",
	}
	if fmt.Sprint(transitions.values) != fmt.Sprint(wantTransitions) {
		t.Fatalf("transitions = %v", transitions.values)
	}
}

func TestManagerUsesWorstMetricAndDeniesGrowthAtCritical(t *testing.T) {
	t.Parallel()

	collector := &collectorStub{usage: usageAt(1_000, 9_500)}
	manager, err := New(Config{
		DataRoot: "/var/lib/platformd", ReservePath: "/var/lib/platformd/.reserve",
		Collector: collector, Reserve: &reserveStub{}, Freezer: &freezerStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.PermitGrowth(context.Background()); !errors.Is(err, ErrGrowthDenied) {
		t.Fatalf("critical inode growth permit = %v", err)
	}
	snapshot, ready := manager.Snapshot()
	if !ready || snapshot.Level != Critical || snapshot.Usage.InodeBasisPoints != 9_500 {
		t.Fatalf("critical inode snapshot = %+v/%v", snapshot, ready)
	}
}

func TestUsedBasisPointsAvoidsCapacityOverflow(t *testing.T) {
	t.Parallel()

	if got := usedBasisPoints(^uint64(0)-1, ^uint64(0)); got != 9_999 {
		t.Fatalf("large filesystem usage = %d", got)
	}
}
