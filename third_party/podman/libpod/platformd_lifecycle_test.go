//go:build !remote

package libpod

import "testing"

func TestPlatformdLifecycleOptions(t *testing.T) {
	runtime := &Runtime{
		manageSignals: true,
		conmonCleanup: true,
	}

	if err := WithSignalHandling(false)(runtime); err != nil {
		t.Fatalf("disable signal handling: %v", err)
	}
	if err := WithConmonCleanupCommand(false)(runtime); err != nil {
		t.Fatalf("disable conmon cleanup command: %v", err)
	}
	if runtime.manageSignals {
		t.Fatal("libpod signal handling remains enabled")
	}
	if runtime.conmonCleanup {
		t.Fatal("conmon cleanup command remains enabled")
	}
}

func TestPlatformdLogRotationOption(t *testing.T) {
	container := &Container{config: &ContainerConfig{}}
	if err := WithLogRotation(4)(container); err != nil {
		t.Fatalf("enable log rotation: %v", err)
	}
	if !container.config.LogRotate || container.config.LogMaxFiles != 4 {
		t.Fatalf("unexpected log rotation config: %+v", container.config)
	}
}
