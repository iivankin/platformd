package servicerestart

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

type waitEngine struct {
	exit chan struct{}
}

func (engine *waitEngine) WaitContainer(ctx context.Context, _ string) (int32, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-engine.exit:
		return 1, nil
	}
}

type restartController struct {
	mu              sync.Mutex
	prepareCalls    int
	restoreFailures int
}

func (controller *restartController) PrepareUnexpectedExit(context.Context, string, string, string) (bool, error) {
	controller.mu.Lock()
	controller.prepareCalls++
	controller.mu.Unlock()
	return true, nil
}

func (controller *restartController) RestoreCurrent(context.Context, string, string) (bool, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.restoreFailures > 0 {
		controller.restoreFailures--
		return false, errors.New("restore failed")
	}
	return true, nil
}

func TestManagerUsesFixedCrashLoopSequenceAndCap(t *testing.T) {
	engine := &waitEngine{exit: make(chan struct{})}
	controller := &restartController{restoreFailures: 6}
	var mu sync.Mutex
	var delays []time.Duration
	done := make(chan struct{})
	manager, err := New(Config{
		Context: context.Background(), Engine: engine, Controller: controller,
		Sleep: func(_ context.Context, delay time.Duration) error {
			mu.Lock()
			delays = append(delays, delay)
			mu.Unlock()
			return nil
		},
		OnResult: func(_ string, err error) {
			if err == nil {
				close(done)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.Publish("service", "deployment", "container")
	close(engine.exit)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("restart did not complete")
	}
	mu.Lock()
	got := slices.Clone(delays)
	mu.Unlock()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	if !slices.Equal(got, want) {
		t.Fatalf("restart delays = %v, want %v", got, want)
	}
}

func TestManagerResetsSequenceAfterFiveMinutesAndIgnoresWithdraw(t *testing.T) {
	current := time.Unix(100, 0)
	engine := &waitEngine{exit: make(chan struct{})}
	controller := &restartController{}
	delay := make(chan time.Duration, 1)
	manager, err := New(Config{
		Context: context.Background(), Engine: engine, Controller: controller,
		Now: func() time.Time { return current },
		Sleep: func(_ context.Context, duration time.Duration) error {
			delay <- duration
			return context.Canceled
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	manager.crashes["service"] = crashState{deploymentID: "deployment", attempts: 5}
	manager.Publish("service", "deployment", "container")
	current = current.Add(5 * time.Minute)
	close(engine.exit)
	select {
	case got := <-delay:
		if got != time.Second {
			t.Fatalf("delay after stable runtime = %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("restart delay was not selected")
	}

	otherEngine := &waitEngine{exit: make(chan struct{})}
	otherController := &restartController{}
	other, err := New(Config{Context: context.Background(), Engine: otherEngine, Controller: otherController})
	if err != nil {
		t.Fatal(err)
	}
	other.Publish("service", "deployment", "container")
	other.Withdraw("service")
	other.Close()
	time.Sleep(10 * time.Millisecond)
	otherController.mu.Lock()
	prepareCalls := otherController.prepareCalls
	otherController.mu.Unlock()
	if prepareCalls != 0 {
		t.Fatalf("planned withdraw prepared %d restarts", prepareCalls)
	}
}
