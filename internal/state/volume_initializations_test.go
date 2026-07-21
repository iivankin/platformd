package state

import (
	"context"
	"errors"
	"testing"
)

func TestVolumeInitializationIsDurableAndFirstWriteWins(t *testing.T) {
	t.Parallel()
	store := openPersistentVolumeStore(t)
	defer store.Close()
	createVolumeTestService(t, store)
	createVolumeTestVolume(t, store)

	initialized, err := store.VolumeInitialized(context.Background(), "project", "service", "volume")
	if err != nil || initialized {
		t.Fatalf("initial volume state = %t, %v", initialized, err)
	}
	if err := store.RecordVolumeInitialization(context.Background(), "project", "service", "volume", 10); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordVolumeInitialization(context.Background(), "project", "service", "volume", 20); err != nil {
		t.Fatal(err)
	}
	initialized, err = store.VolumeInitialized(context.Background(), "project", "service", "volume")
	if err != nil || !initialized {
		t.Fatalf("recorded volume state = %t, %v", initialized, err)
	}
	var initializedAt int64
	if err := store.QueryRowContext(context.Background(),
		"SELECT initialized_at FROM volume_initializations WHERE volume_id = 'volume'",
	).Scan(&initializedAt); err != nil || initializedAt != 10 {
		t.Fatalf("initialized at = %d, %v", initializedAt, err)
	}
}

func TestVolumeInitializationRejectsForeignScope(t *testing.T) {
	t.Parallel()
	store := openPersistentVolumeStore(t)
	defer store.Close()
	createVolumeTestService(t, store)
	createVolumeTestVolume(t, store)

	_, err := store.VolumeInitialized(context.Background(), "project", "other", "volume")
	if !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("foreign scope error = %v", err)
	}
}
