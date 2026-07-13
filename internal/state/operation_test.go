package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOperationIsObservationalAndTransitionsOnce(t *testing.T) {
	t.Parallel()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.BeginOperation(ctx, BeginOperation{
		ID: "operation-1", Kind: "object_store_restore", TargetID: "store-1",
		Progress: "validating", StartedAtMillis: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetOperationProgress(ctx, "operation-1", "installing_payloads"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishOperation(ctx, FinishOperation{
		ID: "operation-1", Status: "succeeded", Progress: "complete", FinishedAtMillis: 20,
	}); err != nil {
		t.Fatal(err)
	}
	operation, err := store.Operation(ctx, "operation-1")
	if err != nil || operation.Status != "succeeded" || operation.Progress != "complete" || operation.FinishedAtMillis != 20 {
		t.Fatalf("operation = %+v, %v", operation, err)
	}
	if err := store.FinishOperation(ctx, FinishOperation{
		ID: "operation-1", Status: "failed", ErrorCode: "late", ErrorMessage: "late failure",
		FinishedAtMillis: 30,
	}); !errors.Is(err, ErrOperationFinished) {
		t.Fatalf("second terminal transition error = %v", err)
	}
	if err := store.SetOperationProgress(ctx, "operation-1", "late progress"); !errors.Is(err, ErrOperationFinished) {
		t.Fatalf("late progress error = %v", err)
	}
}
