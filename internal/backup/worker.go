package backup

import (
	"context"
	"errors"
	"time"
)

const defaultWorkerRetryDelay = 5 * time.Second

type ControlRunner interface {
	RunControl(context.Context) error
}

type Worker struct {
	dirty      *DirtyTracker
	control    ControlRunner
	retryDelay time.Duration
	onError    func(error)
}

func NewWorker(dirty *DirtyTracker, control ControlRunner, retryDelay time.Duration, onError func(error)) (*Worker, error) {
	if dirty == nil || control == nil || retryDelay < 0 {
		return nil, errors.New("backup worker configuration is incomplete")
	}
	if retryDelay == 0 {
		retryDelay = defaultWorkerRetryDelay
	}
	return &Worker{dirty: dirty, control: control, retryDelay: retryDelay, onError: onError}, nil
}

func (worker *Worker) Run(ctx context.Context) error {
	for {
		since, exists := worker.dirty.Take()
		if !exists {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-worker.dirty.Wake():
				continue
			}
		}
		err := worker.control.RunControl(ctx)
		if err == nil {
			continue
		}
		if errors.Is(err, ErrControlTargetMissing) {
			continue
		}
		if worker.onError != nil {
			worker.onError(err)
		}
		var published *PublishedControlError
		if errors.As(err, &published) {
			continue
		}
		worker.dirty.Retry(since)
		select {
		case <-worker.dirty.Wake():
		default:
		}
		timer := time.NewTimer(worker.retryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		case <-worker.dirty.Wake():
			if !timer.Stop() {
				<-timer.C
			}
		}
	}
}
