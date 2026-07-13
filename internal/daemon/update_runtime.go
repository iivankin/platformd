package daemon

import (
	"context"
	"errors"
)

func (stack *runtimeStack) QuiesceWorkloads(ctx context.Context) (func(context.Context) error, error) {
	stack.mu.Lock()
	services := stack.deployments
	redis := stack.managedRedis
	postgres := stack.managedPostgres
	closed := stack.closed
	stack.mu.Unlock()
	if closed {
		return nil, errors.New("container runtime is closed")
	}

	resumes := make([]func(context.Context) error, 0, 3)
	quiesce := func(run func(context.Context) (func(context.Context) error, error)) error {
		resume, err := run(ctx)
		if resume != nil {
			resumes = append(resumes, resume)
		}
		return err
	}
	if services != nil {
		if err := quiesce(services.QuiesceAll); err != nil {
			return resumeInReverse(resumes), err
		}
	}
	if redis != nil {
		if err := quiesce(redis.QuiesceAll); err != nil {
			return resumeInReverse(resumes), err
		}
	}
	if postgres != nil {
		if err := quiesce(postgres.QuiesceAll); err != nil {
			return resumeInReverse(resumes), err
		}
	}
	return resumeInReverse(resumes), nil
}

func resumeInReverse(resumes []func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		failures := make([]error, 0, len(resumes))
		for index := len(resumes) - 1; index >= 0; index-- {
			if err := resumes[index](ctx); err != nil {
				failures = append(failures, err)
			}
		}
		return errors.Join(failures...)
	}
}
