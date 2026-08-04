package diskusage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type resourcePathSourceStub struct {
	paths []ResourcePath
}

func (source resourcePathSourceStub) ResourceDiskPaths(context.Context) ([]ResourcePath, error) {
	return source.paths, nil
}

func TestResourceScannerPublishesPerResourceVolumeTotals(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.Mkdir(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first, "data"), make([]byte, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "data"), make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	scanner, err := NewResourceScanner(resourcePathSourceStub{paths: []ResourcePath{
		{Kind: "service", ResourceID: "api", ProjectID: "project", Paths: []string{first, second}},
		{Kind: "redis", ResourceID: "cache", ProjectID: "project"},
	}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	scanner.entriesPerSecond = 0
	now := time.Unix(100, 0)
	scanner.now = func() time.Time { return now }
	if err := scanner.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := scanner.Resources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CheckedAt != now || len(snapshot.Resources) != 2 ||
		snapshot.Resources[0].Bytes < 12_288 || snapshot.Resources[1].Bytes != 0 {
		t.Fatalf("resource disk snapshot = %+v", snapshot)
	}
}

func TestScannerReturnsEmptySnapshotBeforeBackgroundRefresh(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	scanner, err := NewScanner([]Path{{ID: "data", Path: root}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := scanner.Components(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.CheckedAt.IsZero() {
		t.Fatalf("checked at = %s, want zero", snapshot.CheckedAt)
	}
	if len(snapshot.Components) != 1 || snapshot.Components[0] != (Component{ID: "data"}) {
		t.Fatalf("components = %+v", snapshot.Components)
	}
}

func TestScannerRefreshesOnlyInvalidatedComponents(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	missing := filepath.Join(root, "missing")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "first"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	scanner, err := NewScanner([]Path{
		{ID: "data", Path: data},
		{ID: "missing", Path: missing},
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	scanner.entriesPerSecond = 0
	now := time.Unix(100, 0)
	scanner.now = func() time.Time { return now }
	if err := scanner.refreshDirty(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	first, err := scanner.Components(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Components) != 2 || first.Components[0].Bytes == 0 || first.Components[1].Bytes != 0 {
		t.Fatalf("first component usage = %+v", first.Components)
	}
	if first.CheckedAt != now {
		t.Fatalf("first checked at = %s, want %s", first.CheckedAt, now)
	}

	if err := os.WriteFile(filepath.Join(data, "second"), make([]byte, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(missing, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(missing, "new"), make([]byte, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	scanner.Invalidate("data")
	if err := scanner.refreshDirty(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	refreshed, err := scanner.Components(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Components[0].Bytes <= first.Components[0].Bytes {
		t.Fatalf("refreshed data bytes = %d, want > %d", refreshed.Components[0].Bytes, first.Components[0].Bytes)
	}
	if refreshed.Components[1].Bytes != 0 {
		t.Fatalf("non-invalidated component bytes = %d, want 0", refreshed.Components[1].Bytes)
	}
	if refreshed.CheckedAt != first.CheckedAt {
		t.Fatalf("snapshot checked at = %s, want oldest measurement %s", refreshed.CheckedAt, first.CheckedAt)
	}
}

func TestScannerRunRefreshesInBackground(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	scanner, err := NewScanner([]Path{{ID: "data", Path: root}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	scanner.entriesPerSecond = 0
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scanner.Run(ctx, nil) }()

	deadline := time.Now().Add(time.Second)
	for {
		snapshot, readErr := scanner.Components(context.Background())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !snapshot.CheckedAt.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background scan did not publish a snapshot")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("run error = %v, want context canceled", err)
	}
}
