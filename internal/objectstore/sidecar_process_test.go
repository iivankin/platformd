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
	}, "/new-volume", "/new-socket")
	want := []string{
		"PATH=/bin",
		"PLATFORMD_OBJECTSTORE_VOLUME=/new-volume",
		"PLATFORMD_OBJECTSTORE_SOCKET=/new-socket",
		"RUSTFS_DURABILITY_MODE=none",
	}
	if !slices.Equal(environment, want) {
		t.Fatalf("sidecar environment = %v, want %v", environment, want)
	}
}
