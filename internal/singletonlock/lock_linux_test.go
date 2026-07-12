//go:build linux

package singletonlock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireExcludesSecondDaemon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locks", "daemon.lock")
	first, err := Acquire(path, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := Acquire(path, os.Geteuid()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second acquire error = %v, want ErrAlreadyRunning", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(path, os.Geteuid())
	if err != nil {
		t.Fatalf("reacquire released lock: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireRejectsUnsafeExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path, os.Geteuid()); err == nil {
		t.Fatal("expected unsafe lock mode to fail")
	}
}

func TestAcquireRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "daemon.lock")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path, os.Geteuid()); err == nil {
		t.Fatal("expected symlink lock path to fail")
	}
}
