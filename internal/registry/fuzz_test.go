package registry

import "testing"

func FuzzRegistryInputs(f *testing.F) {
	f.Add(
		"/v2/team/app/manifests/latest",
		"repository:team/app:pull,push",
		"Bearer token",
		"bytes=0-15",
		OCIImageManifestMediaType,
		[]byte(`{"schemaVersion":2,"config":{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`),
	)
	f.Add("/v2/", "", "", "bytes=-1", "application/json", []byte("{"))

	f.Fuzz(func(
		t *testing.T,
		path string,
		scope string,
		authorization string,
		rangeValue string,
		contentType string,
		manifest []byte,
	) {
		if len(path)+len(scope)+len(authorization)+len(rangeValue)+len(contentType) > 128<<10 ||
			len(manifest) > MaximumManifestSize+1 {
			t.Skip()
		}

		_, _ = parseRegistryRoute(path)
		_, _, _ = parseTokenScope(scope)
		_, _ = bearerAuthorization([]string{authorization})
		_, _, _, _ = parseRegistryRange(rangeValue, int64(len(manifest))+1)
		_, _, _, _ = validateManifest(contentType, manifest)
	})
}
