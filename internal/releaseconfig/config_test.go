package releaseconfig_test

import (
	"testing"

	"github.com/iivankin/platformd/internal/releaseconfig"
)

func TestEmbeddedReleaseConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := releaseconfig.PublicKey(); err != nil {
		t.Fatal(err)
	}
	if got := releaseconfig.VersionManifestURL("1.2.3"); got != "https://github.com/iivankin/platformd/releases/download/v1.2.3/platformd-linux-amd64.manifest.json" {
		t.Fatalf("manifest URL = %q", got)
	}
}
