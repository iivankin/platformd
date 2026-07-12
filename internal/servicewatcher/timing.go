package servicewatcher

import (
	"context"
	"time"
)

const (
	RemoteInterval       = time.Minute
	RemoteMaximumBackoff = 15 * time.Minute
	EmbeddedRetry        = 10 * time.Second
	EmbeddedMaximumRetry = time.Minute
)

func exponentialDelay(base, maximum time.Duration, failures int) time.Duration {
	if failures < 1 {
		return base
	}
	delay := base
	for index := 1; index < failures; index++ {
		if delay >= maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func wait(ctx context.Context, wake <-chan struct{}, delay time.Duration) bool {
	if delay == 0 {
		select {
		case <-ctx.Done():
			return false
		case <-wake:
			return true
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		return true
	case <-timer.C:
		return true
	}
}

func coalesce(channel chan<- struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}
