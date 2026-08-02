package containerengine

import (
	"sort"
	"time"
)

type imageGarbageCollectKind uint8

const (
	imageGarbageCollectBuildCache imageGarbageCollectKind = iota
	imageGarbageCollectFinal
)

type imageGarbageCollectCandidate struct {
	id       string
	digests  []string
	cachedAt time.Time
	readOnly bool
	final    bool
	kind     imageGarbageCollectKind
}

func selectImageGarbageCollectCandidates(
	images []imageGarbageCollectCandidate,
	finalBefore, buildCacheBefore time.Time,
	protectedIDs, protectedDigests map[string]struct{},
) []imageGarbageCollectCandidate {
	result := make([]imageGarbageCollectCandidate, 0, len(images))
	for _, image := range images {
		if image.id == "" || image.cachedAt.IsZero() || image.readOnly {
			continue
		}
		if _, protected := protectedIDs[image.id]; protected {
			continue
		}
		protected := false
		for _, digest := range image.digests {
			if _, protected = protectedDigests[digest]; protected {
				break
			}
		}
		if protected {
			continue
		}
		cutoff := buildCacheBefore
		image.kind = imageGarbageCollectBuildCache
		if image.final {
			cutoff = finalBefore
			image.kind = imageGarbageCollectFinal
		}
		if cutoff.IsZero() || !image.cachedAt.Before(cutoff) {
			continue
		}
		result = append(result, image)
	}
	// Build children are normally newer than their base. Removing them first
	// lets non-forced removal reclaim the now-unreferenced base later in the pass.
	sort.Slice(result, func(left, right int) bool {
		return result[left].cachedAt.After(result[right].cachedAt)
	})
	return result
}
