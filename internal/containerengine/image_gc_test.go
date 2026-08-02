package containerengine

import (
	"testing"
	"time"
)

func TestImageGarbageCollectionUsesSeparateFinalAndBuildCacheRetention(t *testing.T) {
	t.Parallel()
	finalBefore := time.Unix(100, 0)
	buildCacheBefore := time.Unix(200, 0)
	images := []imageGarbageCollectCandidate{
		{id: "old-final", digests: []string{"sha256:old-final"}, cachedAt: finalBefore.Add(-time.Hour), final: true},
		{id: "young-final", digests: []string{"sha256:young-final"}, cachedAt: finalBefore, final: true},
		{id: "old-cache", digests: []string{"sha256:old-cache"}, cachedAt: buildCacheBefore.Add(-time.Hour)},
		{id: "young-cache", digests: []string{"sha256:young-cache"}, cachedAt: buildCacheBefore},
		{id: "used-id", digests: []string{"sha256:used-id"}, cachedAt: finalBefore.Add(-time.Hour), final: true},
		{id: "used-digest", digests: []string{"sha256:used-digest"}, cachedAt: finalBefore.Add(-time.Hour), final: true},
		{id: "readonly", digests: []string{"sha256:readonly"}, cachedAt: finalBefore.Add(-time.Hour), readOnly: true},
		{id: "unknown-age", digests: []string{"sha256:unknown"}},
	}
	selected := selectImageGarbageCollectCandidates(
		images, finalBefore, buildCacheBefore,
		map[string]struct{}{"used-id": {}},
		map[string]struct{}{"sha256:used-digest": {}},
	)
	if len(selected) != 2 ||
		selected[0].id != "old-cache" || selected[0].kind != imageGarbageCollectBuildCache ||
		selected[1].id != "old-final" || selected[1].kind != imageGarbageCollectFinal {
		t.Fatalf("selected images = %+v", selected)
	}
}
