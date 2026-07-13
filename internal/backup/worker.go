package backup

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

const defaultWorkerRetryDelay = 5 * time.Second

var ErrWorkerBusy = errors.New("backup worker is busy")

type ControlRunner interface {
	RunControl(context.Context) error
}

type ResourceRunner interface {
	RunResource(context.Context, string, string, *int64, int) (state.BackupRecord, error)
	RunResourceStarted(context.Context, string, string, *int64, int, func(state.BackupRecord)) (state.BackupRecord, error)
}

type WorkerConfig struct {
	Dirty      *DirtyTracker
	Control    ControlRunner
	Store      ScheduleStore
	Resources  ResourceRunner
	StartedAt  time.Time
	Now        func() time.Time
	RetryDelay time.Duration
	OnError    func(error)
}

type Worker struct {
	dirty      *DirtyTracker
	control    ControlRunner
	store      ScheduleStore
	resources  ResourceRunner
	startedAt  time.Time
	now        func() time.Time
	retryDelay time.Duration
	onError    func(error)
	mutex      sync.Mutex
	busy       bool
	manual     chan manualRequest
	manualWake chan struct{}
}

type manualRequest struct {
	resourceKind   string
	resourceID     string
	retentionCount int
	started        chan manualStartResult
}

type manualStartResult struct {
	record state.BackupRecord
	err    error
}

func NewWorker(dirty *DirtyTracker, control ControlRunner, retryDelay time.Duration, onError func(error)) (*Worker, error) {
	return newWorker(WorkerConfig{Dirty: dirty, Control: control, RetryDelay: retryDelay, OnError: onError})
}

func NewScheduledWorker(config WorkerConfig) (*Worker, error) {
	if config.Store == nil || config.Resources == nil || config.StartedAt.IsZero() {
		return nil, errors.New("scheduled backup worker dependencies are incomplete")
	}
	return newWorker(config)
}

func newWorker(config WorkerConfig) (*Worker, error) {
	if config.Dirty == nil || config.Control == nil || config.RetryDelay < 0 {
		return nil, errors.New("backup worker configuration is incomplete")
	}
	if (config.Store == nil) != (config.Resources == nil) {
		return nil, errors.New("backup schedule store and resource runner must be configured together")
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = defaultWorkerRetryDelay
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Worker{
		dirty: config.Dirty, control: config.Control, store: config.Store, resources: config.Resources,
		startedAt: config.StartedAt, now: config.Now, retryDelay: config.RetryDelay, onError: config.OnError,
		manual: make(chan manualRequest, 1), manualWake: make(chan struct{}, 1),
	}, nil
}

func (worker *Worker) TryRunNow(
	ctx context.Context,
	resourceKind, resourceID string,
	retentionCount int,
) (state.BackupRecord, error) {
	if worker.resources == nil {
		return state.BackupRecord{}, errors.New("resource backup worker is not configured")
	}
	worker.mutex.Lock()
	if worker.busy {
		worker.mutex.Unlock()
		return state.BackupRecord{}, ErrWorkerBusy
	}
	worker.busy = true
	worker.mutex.Unlock()
	request := manualRequest{
		resourceKind: resourceKind, resourceID: resourceID, retentionCount: retentionCount,
		started: make(chan manualStartResult, 1),
	}
	worker.manual <- request
	worker.signalManual()
	select {
	case <-ctx.Done():
		return state.BackupRecord{}, ctx.Err()
	case result := <-request.started:
		return result.record, result.err
	}
}

func (worker *Worker) Run(ctx context.Context) error {
	for {
		select {
		case request := <-worker.manual:
			worker.runManual(ctx, request)
			worker.finishBusy()
			continue
		default:
		}
		candidate, exists, err := worker.nextCandidate(ctx)
		if err != nil {
			worker.report(err)
			if err := worker.wait(ctx, worker.retryDelay); err != nil {
				return err
			}
			continue
		}
		if !exists {
			if err := worker.wait(ctx, worker.nextMinuteDelay()); err != nil {
				return err
			}
			continue
		}
		if !worker.tryBeginScheduled() {
			continue
		}
		if candidate.ResourceKind == "control" {
			since, dirty := worker.dirty.Take()
			if !dirty {
				worker.finishBusy()
				continue
			}
			if err := worker.runControl(ctx, since); err != nil {
				worker.finishBusy()
				return err
			}
			worker.finishBusy()
			continue
		}
		record, runErr := worker.resources.RunResource(
			ctx, candidate.ResourceKind, candidate.ResourceID,
			candidate.ScheduledOccurrenceMillis, candidate.RetentionCount,
		)
		if runErr == nil {
			worker.finishBusy()
			continue
		}
		var published *PublishedResourceError
		if !errors.As(runErr, &published) && !errors.Is(runErr, ErrResourceTargetMissing) {
			worker.report(runErr)
		}
		if record.ID == "" {
			if err := worker.wait(ctx, worker.retryDelay); err != nil {
				worker.finishBusy()
				return err
			}
		}
		worker.finishBusy()
	}
}

func (worker *Worker) runManual(ctx context.Context, request manualRequest) {
	started := false
	record, err := worker.resources.RunResourceStarted(
		ctx, request.resourceKind, request.resourceID, nil, request.retentionCount,
		func(record state.BackupRecord) {
			started = true
			request.started <- manualStartResult{record: record}
		},
	)
	if !started {
		request.started <- manualStartResult{record: record, err: err}
	}
	if err != nil {
		var published *PublishedResourceError
		if !errors.As(err, &published) && !errors.Is(err, ErrResourceTargetMissing) {
			worker.report(err)
		}
	}
}

func (worker *Worker) tryBeginScheduled() bool {
	worker.mutex.Lock()
	defer worker.mutex.Unlock()
	if worker.busy {
		return false
	}
	worker.busy = true
	return true
}

func (worker *Worker) finishBusy() {
	worker.mutex.Lock()
	worker.busy = false
	worker.mutex.Unlock()
}

func (worker *Worker) signalManual() {
	select {
	case worker.manualWake <- struct{}{}:
	default:
	}
}

func (worker *Worker) nextCandidate(ctx context.Context) (DueCandidate, bool, error) {
	dirtySince, dirty := worker.dirty.Peek()
	if worker.store == nil {
		if !dirty {
			return DueCandidate{}, false, nil
		}
		return DueCandidate{ResourceKind: "control", DueAt: dirtySince}, true, nil
	}
	return SelectDueCandidate(ctx, worker.store, dirtySince, dirty, worker.startedAt, worker.now())
}

func (worker *Worker) runControl(ctx context.Context, since time.Time) error {
	err := worker.control.RunControl(ctx)
	if err == nil || errors.Is(err, ErrControlTargetMissing) {
		return nil
	}
	worker.report(err)
	var published *PublishedControlError
	if errors.As(err, &published) {
		return nil
	}
	worker.dirty.Retry(since)
	return worker.wait(ctx, worker.retryDelay)
}

func (worker *Worker) report(err error) {
	if worker.onError != nil {
		worker.onError(err)
	}
}

func (worker *Worker) nextMinuteDelay() time.Duration {
	now := worker.now()
	delay := now.UTC().Truncate(time.Minute).Add(time.Minute).Sub(now)
	if delay <= 0 {
		return time.Minute
	}
	return delay
}

func (worker *Worker) wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	case <-worker.dirty.Wake():
		return nil
	case <-worker.manualWake:
		return nil
	}
}
