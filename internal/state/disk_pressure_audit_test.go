package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/diskpressure"
)

func TestDiskPressureTransitionAuditIsObservational(t *testing.T) {
	t.Parallel()

	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	err = store.AppendDiskPressureAudit(context.Background(), DiskPressureAuditInput{
		ID: "audit", InstallationID: "installation", From: diskpressure.Low, To: diskpressure.Critical,
		Usage:           diskpressure.Usage{TotalBytes: 100, AvailableBytes: 4, TotalInodes: 1000, AvailableInodes: 500, ByteBasisPoints: 9600, InodeBasisPoints: 5000},
		CreatedAtMillis: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	var action, metadata string
	if err := store.QueryRowContext(context.Background(), "SELECT action, metadata_json FROM audit_events WHERE id = 'audit'").Scan(&action, &metadata); err != nil {
		t.Fatal(err)
	}
	if action != "disk_pressure.transition" || !strings.Contains(metadata, `"from":"low"`) || !strings.Contains(metadata, `"to":"critical"`) || !strings.Contains(metadata, `"byteBasisPoints":9600`) {
		t.Fatalf("disk pressure audit = %s %s", action, metadata)
	}
}
