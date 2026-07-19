package containerengine

import (
	"testing"
	"time"
)

func TestImageGarbageCollectionOnlySelectsOldUnreferencedWritableImages(t *testing.T) {
	t.Parallel()
	before := time.Unix(100, 0)
	images := []imageGarbageCollectCandidate{
		{id: "old", digests: []string{"sha256:old"}, cachedAt: before.Add(-time.Hour)},
		{id: "used-id", digests: []string{"sha256:used-id"}, cachedAt: before.Add(-time.Hour)},
		{id: "used-digest", digests: []string{"sha256:used-digest"}, cachedAt: before.Add(-time.Hour)},
		{id: "new", digests: []string{"sha256:new"}, cachedAt: before},
		{id: "readonly", digests: []string{"sha256:readonly"}, cachedAt: before.Add(-time.Hour), readOnly: true},
		{id: "unknown-age", digests: []string{"sha256:unknown"}},
	}
	selected := selectImageGarbageCollectCandidates(
		images, before,
		map[string]struct{}{"used-id": {}},
		map[string]struct{}{"sha256:used-digest": {}},
	)
	if len(selected) != 1 || selected[0].id != "old" {
		t.Fatalf("selected images = %+v", selected)
	}
}
