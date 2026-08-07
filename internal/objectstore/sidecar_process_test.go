package objectstore

import (
	"slices"
	"testing"
)

func TestSidecarEnvironmentDisablesSynchronousObjectDurability(t *testing.T) {
	environment := sidecarEnvironment([]string{
		"PATH=/bin",
		"PLATFORMD_OBJECTSTORE_VOLUME=old-volume",
		"PLATFORMD_OBJECTSTORE_SOCKET=old-socket",
		"RUSTFS_DURABILITY_MODE=strict",
		"RUSTFS_SCANNER_SPEED=fastest",
		"RUSTFS_SCANNER_CYCLE=60",
	}, "/new-volume", "/new-socket")
	want := []string{
		"PATH=/bin",
		"PLATFORMD_OBJECTSTORE_VOLUME=/new-volume",
		"PLATFORMD_OBJECTSTORE_SOCKET=/new-socket",
		"RUSTFS_DURABILITY_MODE=none",
		"RUSTFS_SCANNER_SPEED=slow",
		"RUSTFS_SCANNER_CYCLE=1800",
	}
	if !slices.Equal(environment, want) {
		t.Fatalf("sidecar environment = %v, want %v", environment, want)
	}
}
