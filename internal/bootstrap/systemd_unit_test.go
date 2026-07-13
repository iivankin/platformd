package bootstrap

import (
	"strings"
	"testing"
)

func TestPlatformdUnitPreservesExecutableImageDirectories(t *testing.T) {
	if !strings.Contains(platformdUnit, "\nUMask=0022\n") {
		t.Fatal("platformd unit must use UMask=0022 so containers/storage can create traversable image roots")
	}
	if strings.Contains(platformdUnit, "\nUMask=0077\n") {
		t.Fatal("UMask=0077 makes non-root OCI image processes unable to traverse unpacked layer roots")
	}
}
