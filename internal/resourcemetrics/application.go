package resourcemetrics

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/state"
)

const (
	SampleInterval = time.Minute
	Retention      = 30 * 24 * time.Hour
)

var ErrInvalidRange = errors.New("invalid resource metrics range")

type Store interface {
	ResourceMetricTargets(context.Context) ([]state.ResourceMetricTarget, error)
	RecordResourceMetricSamples(context.Context, []state.ResourceMetricSample) error
	ResourceMetricSamples(context.Context, string, string, int64, int64) ([]state.ResourceMetricSample, error)
	DeleteResourceMetricSamplesBefore(context.Context, int64) error
}

type UsageReader interface {
	Read(cgroupstats.Kind, string) (cgroupstats.Sample, error)
}

type NetworkCounters struct {
	RXBytes uint64
	TXBytes uint64
}

type NetworkReader interface {
	ReadResourceNetwork(cgroupstats.Kind, string) (NetworkCounters, bool, error)
}

type Current struct {
	cgroupstats.Sample
	NetworkRXBytes   uint64
	NetworkTXBytes   uint64
	NetworkAvailable bool
}

type Point struct {
	ObservedAt                   int64
	CPUMillicores                *int64
	MemoryBytes                  uint64
	NetworkIngressBytesPerSecond *int64
	NetworkEgressBytesPerSecond  *int64
	Running                      bool
}

type History struct {
	From       int64
	To         int64
	StepMillis int64
	Points     []Point
}

type Config struct {
	Interval  time.Duration
	Retention time.Duration
	Now       func() time.Time
}

type Application struct {
	store     Store
	usage     UsageReader
	network   NetworkReader
	interval  time.Duration
	retention time.Duration
	now       func() time.Time
}

func NewApplication(store Store, usage UsageReader, network NetworkReader, config Config) (*Application, error) {
	if store == nil || usage == nil || network == nil {
		return nil, errors.New("resource metrics dependencies are incomplete")
	}
	if config.Interval == 0 {
		config.Interval = SampleInterval
	}
	if config.Retention == 0 {
		config.Retention = Retention
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Interval <= 0 || config.Retention < config.Interval {
		return nil, errors.New("resource metrics interval or retention is invalid")
	}
	return &Application{
		store: store, usage: usage, network: network,
		interval: config.Interval, retention: config.Retention, now: config.Now,
	}, nil
}

func (application *Application) Read(kind cgroupstats.Kind, resourceID string) (Current, error) {
	sample, err := application.usage.Read(kind, resourceID)
	if err != nil {
		return Current{}, err
	}
	current := Current{Sample: sample}
	if !sample.Running {
		return current, nil
	}
	counters, available, err := application.network.ReadResourceNetwork(kind, resourceID)
	// CPU and memory remain useful when Podman cannot momentarily enter a container netns.
	if err == nil && available {
		current.NetworkRXBytes = counters.RXBytes
		current.NetworkTXBytes = counters.TXBytes
		current.NetworkAvailable = true
	}
	return current, nil
}

func (application *Application) History(ctx context.Context, kind cgroupstats.Kind, resourceID string, window time.Duration) (History, error) {
	if err := cgroupstats.ValidateResource(kind, resourceID); err != nil {
		return History{}, err
	}
	step, err := stepForWindow(window)
	if err != nil {
		return History{}, err
	}
	to := application.now().UnixMilli()
	from := to - window.Milliseconds()
	samples, err := application.store.ResourceMetricSamples(ctx, string(kind), resourceID, from, to)
	if err != nil {
		return History{}, err
	}
	return History{
		From: from, To: to, StepMillis: step.Milliseconds(),
		Points: aggregate(samples, from, step),
	}, nil
}

func (application *Application) Run(ctx context.Context, onError func(error)) error {
	if onError == nil {
		onError = func(error) {}
	}
	application.collect(ctx, onError)
	ticker := time.NewTicker(application.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			application.collect(ctx, onError)
		}
	}
}

func (application *Application) collect(ctx context.Context, onError func(error)) {
	targets, err := application.store.ResourceMetricTargets(ctx)
	if err != nil {
		onError(fmt.Errorf("list resource metric targets: %w", err))
		return
	}
	samples := make([]state.ResourceMetricSample, 0, len(targets))
	for _, target := range targets {
		kind := cgroupstats.Kind(target.Kind)
		usage, readErr := application.usage.Read(kind, target.ResourceID)
		if readErr != nil {
			onError(fmt.Errorf("read %s %s metrics: %w", target.Kind, target.ResourceID, readErr))
			continue
		}
		sample := state.ResourceMetricSample{
			Kind: target.Kind, ResourceID: target.ResourceID, ObservedAt: usage.ObservedAtMillis,
			CPUUsageMicros: usage.CPUUsageMicros, MemoryBytes: usage.MemoryBytes, Running: usage.Running,
		}
		if usage.Running {
			counters, available, networkErr := application.network.ReadResourceNetwork(kind, target.ResourceID)
			if networkErr != nil {
				onError(fmt.Errorf("read %s %s network metrics: %w", target.Kind, target.ResourceID, networkErr))
			} else if available {
				sample.NetworkRXBytes = uint64Pointer(counters.RXBytes)
				sample.NetworkTXBytes = uint64Pointer(counters.TXBytes)
			}
		}
		samples = append(samples, sample)
	}
	if err := application.store.RecordResourceMetricSamples(ctx, samples); err != nil {
		onError(fmt.Errorf("record resource metrics: %w", err))
	}
	cutoff := application.now().Add(-application.retention).UnixMilli()
	if err := application.store.DeleteResourceMetricSamplesBefore(ctx, cutoff); err != nil {
		onError(fmt.Errorf("apply resource metrics retention: %w", err))
	}
}

func stepForWindow(window time.Duration) (time.Duration, error) {
	switch window {
	case time.Hour:
		return time.Minute, nil
	case 6 * time.Hour:
		return 5 * time.Minute, nil
	case 24 * time.Hour:
		return 15 * time.Minute, nil
	case 7 * 24 * time.Hour:
		return time.Hour, nil
	case 30 * 24 * time.Hour:
		return 6 * time.Hour, nil
	default:
		return 0, ErrInvalidRange
	}
}

type bucket struct {
	observedAt int64
	memory     uint64
	memoryN    uint64
	cpu        int64
	cpuN       int64
	rx         int64
	rxN        int64
	tx         int64
	txN        int64
	running    bool
}

func aggregate(samples []state.ResourceMetricSample, from int64, step time.Duration) []Point {
	if len(samples) == 0 {
		return []Point{}
	}
	buckets := make(map[int64]*bucket)
	order := make([]int64, 0)
	for index, current := range samples {
		if current.ObservedAt < from {
			continue
		}
		key := (current.ObservedAt - from) / step.Milliseconds()
		entry := buckets[key]
		if entry == nil {
			entry = &bucket{}
			buckets[key] = entry
			order = append(order, key)
		}
		entry.observedAt = current.ObservedAt
		entry.memory += current.MemoryBytes
		entry.memoryN++
		entry.running = entry.running || current.Running
		if index == 0 {
			continue
		}
		previous := samples[index-1]
		elapsed := current.ObservedAt - previous.ObservedAt
		if elapsed <= 0 || elapsed > (2*SampleInterval).Milliseconds() ||
			!(previous.Running && current.Running) || current.CPUUsageMicros < previous.CPUUsageMicros {
			continue
		}
		entry.cpu += int64(math.Round(float64(current.CPUUsageMicros-previous.CPUUsageMicros) / float64(elapsed)))
		entry.cpuN++
		if previous.NetworkRXBytes == nil || current.NetworkRXBytes == nil ||
			previous.NetworkTXBytes == nil || current.NetworkTXBytes == nil ||
			*current.NetworkRXBytes < *previous.NetworkRXBytes || *current.NetworkTXBytes < *previous.NetworkTXBytes {
			continue
		}
		entry.rx += bytesPerSecond(*current.NetworkRXBytes-*previous.NetworkRXBytes, elapsed)
		entry.rxN++
		entry.tx += bytesPerSecond(*current.NetworkTXBytes-*previous.NetworkTXBytes, elapsed)
		entry.txN++
	}
	points := make([]Point, 0, len(order))
	for _, key := range order {
		entry := buckets[key]
		point := Point{
			ObservedAt: entry.observedAt, MemoryBytes: entry.memory / entry.memoryN,
			Running: entry.running,
		}
		if entry.cpuN != 0 {
			point.CPUMillicores = int64Pointer(entry.cpu / entry.cpuN)
		}
		if entry.rxN != 0 {
			point.NetworkIngressBytesPerSecond = int64Pointer(entry.rx / entry.rxN)
		}
		if entry.txN != 0 {
			point.NetworkEgressBytesPerSecond = int64Pointer(entry.tx / entry.txN)
		}
		points = append(points, point)
	}
	return points
}

func bytesPerSecond(delta uint64, elapsedMillis int64) int64 {
	return int64(math.Round(float64(delta) * 1000 / float64(elapsedMillis)))
}

func uint64Pointer(value uint64) *uint64 { return &value }
func int64Pointer(value int64) *int64    { return &value }
