package diskusage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScannerCachesComponentUsageAndIgnoresMissingPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "first"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	scanner, err := NewScanner([]Path{
		{ID: "data", Path: data},
		{ID: "missing", Path: filepath.Join(root, "missing")},
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	scanner.now = func() time.Time { return now }
	first, err := scanner.Components(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Components) != 2 || first.Components[0].Bytes == 0 || first.Components[1].Bytes != 0 {
		t.Fatalf("first component usage = %+v", first.Components)
	}
	if err := os.WriteFile(filepath.Join(data, "second"), make([]byte, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	cached, err := scanner.Components(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cached.Components[0].Bytes != first.Components[0].Bytes {
		t.Fatalf("cached component bytes = %d, want %d", cached.Components[0].Bytes, first.Components[0].Bytes)
	}
	now = now.Add(time.Hour)
	refreshed, err := scanner.Components(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Components[0].Bytes <= first.Components[0].Bytes {
		t.Fatalf("refreshed component bytes = %d, want > %d", refreshed.Components[0].Bytes, first.Components[0].Bytes)
	}
}
