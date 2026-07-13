package containerlogs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanerAppliesRetentionWithoutRemovingAnActiveBase(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "services", "service", "deployment")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_000_000, 0)
	old := now.Add(-8 * 24 * time.Hour)
	fresh := now.Add(-time.Hour)
	activeBase := cleanupLog(t, directory, "active.log", 11, old)
	activeRotation := cleanupLog(t, directory, "active.log.1", 12, old)
	closedBase := cleanupLog(t, directory, "closed.log", 13, old)
	freshBase := cleanupLog(t, directory, "fresh.log", 14, fresh)
	external := cleanupLog(t, t.TempDir(), "external.log", 15, old)
	if err := os.Symlink(external, filepath.Join(directory, "linked.log")); err != nil {
		t.Fatal(err)
	}

	cleaner, err := NewCleaner(CleanerConfig{Root: root, Retention: 7 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	result, err := cleaner.Sweep(context.Background(), now, map[string]struct{}{
		activeBase: {}, external: {},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedFiles != 2 || result.DeletedBytes != 25 || result.RemainingBytes != 25 {
		t.Fatalf("cleanup result = %+v", result)
	}
	assertLogExists(t, activeBase, true)
	assertLogExists(t, activeRotation, false)
	assertLogExists(t, closedBase, false)
	assertLogExists(t, freshBase, true)
	assertLogExists(t, external, true)
}

func TestCleanerBudgetUsesOldestClosedFilesAndReportsProtectedOverflow(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "redis", "resource")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_000_000, 0)
	oldest := cleanupLog(t, directory, "oldest.log", 40, now.Add(-2*time.Hour))
	newer := cleanupLog(t, directory, "newer.log", 40, now.Add(-time.Hour))
	active := cleanupLog(t, directory, "active.log", 70, now.Add(-3*time.Hour))

	cleaner, err := NewCleaner(CleanerConfig{
		Root: root, Retention: 7 * 24 * time.Hour, BudgetBytes: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := cleaner.Sweep(context.Background(), now, map[string]struct{}{active: {}})
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedFiles != 2 || result.RemainingBytes != 70 || result.BudgetExceeded {
		t.Fatalf("budget cleanup = %+v", result)
	}
	assertLogExists(t, oldest, false)
	assertLogExists(t, newer, false)
	assertLogExists(t, active, true)

	protectedCleaner, err := NewCleaner(CleanerConfig{
		Root: root, Retention: 7 * 24 * time.Hour, BudgetBytes: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err = protectedCleaner.Sweep(context.Background(), now, map[string]struct{}{active: {}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.BudgetExceeded || result.RemainingBytes != 70 || result.DeletedFiles != 0 {
		t.Fatalf("protected budget cleanup = %+v", result)
	}
}

func TestCleanerAcceptsAMissingRootAsEmpty(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	cleaner, err := NewCleaner(CleanerConfig{Root: root, Retention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	result, err := cleaner.Sweep(context.Background(), time.Now(), nil)
	if err != nil || result != (CleanupResult{}) {
		t.Fatalf("missing-root cleanup = %+v, %v", result, err)
	}
}

func cleanupLog(t *testing.T, directory, name string, size int, modified time.Time) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertLogExists(t *testing.T, path string, expected bool) {
	t.Helper()
	_, err := os.Lstat(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if exists != expected {
		t.Fatalf("log %s exists = %t, want %t", path, exists, expected)
	}
}
