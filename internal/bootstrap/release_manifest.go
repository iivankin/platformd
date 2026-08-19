//go:build !platformd_worker

package bootstrap

import "github.com/iivankin/platformd/internal/releaseconfig"

func productionManifestURL(version string) string {
	return releaseconfig.VersionManifestURL(version)
}
