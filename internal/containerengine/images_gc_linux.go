//go:build linux && amd64 && cgo

package containerengine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"go.podman.io/common/libimage"
	"go.podman.io/storage"
)

type orphanLayerCandidate struct {
	id      string
	size    int64
	created time.Time
	depth   int
}

func (e *Engine) GarbageCollectImages(ctx context.Context, request ImageGarbageCollectRequest) (ImageGarbageCollectResult, error) {
	if request.FinalImageBefore.IsZero() || request.BuildCacheBefore.IsZero() {
		return ImageGarbageCollectResult{}, errors.New("image garbage collection cutoffs are incomplete")
	}
	if request.OrphanLayerMaximumAge < 0 {
		return ImageGarbageCollectResult{}, errors.New("orphan layer maximum age cannot be negative")
	}
	defer e.notifyImageStorageChanged()

	e.imageOperations.Lock()
	defer e.imageOperations.Unlock()

	result, imageErr := e.garbageCollectImageRecords(ctx, request)
	orphanResult, orphanErr := e.garbageCollectOrphanLayersLocked(request.OrphanLayerMaximumAge)
	result.OrphanLayersRemoved += orphanResult.OrphanLayersRemoved
	result.RemovedBytes += orphanResult.RemovedBytes
	result.Skipped += orphanResult.Skipped
	return result, errors.Join(imageErr, orphanErr)
}

func (e *Engine) garbageCollectImageRecords(ctx context.Context, request ImageGarbageCollectRequest) (ImageGarbageCollectResult, error) {
	images, err := e.runtime.LibimageRuntime().ListImages(ctx, &libimage.ListImagesOptions{
		Filters: []string{"readonly=false"},
	})
	if err != nil {
		return ImageGarbageCollectResult{}, fmt.Errorf("list cached images: %w", err)
	}
	protectedIDs := make(map[string]struct{})
	containers, err := e.runtime.GetAllContainers()
	if err != nil {
		return ImageGarbageCollectResult{}, fmt.Errorf("list image container references: %w", err)
	}
	for _, container := range containers {
		imageID, _ := container.Image()
		if imageID != "" {
			protectedIDs[imageID] = struct{}{}
		}
	}
	byID := make(map[string]*libimage.Image, len(images))
	for _, image := range images {
		byID[image.ID()] = image
		if imageMatchesDigest(image, request.ProtectedDigests) {
			protectedIDs[image.ID()] = struct{}{}
		}
	}

	candidates := make([]imageGarbageCollectCandidate, 0, len(images))
	for _, image := range images {
		if image.TopLayer() == "" {
			// Manifest-only records do not have a trustworthy local cache age.
			continue
		}
		layer, layerErr := e.store.Layer(image.TopLayer())
		if layerErr != nil {
			return ImageGarbageCollectResult{}, fmt.Errorf("read cached image layer %s: %w", image.TopLayer(), layerErr)
		}
		// Buildah cache is the union of dangling leaves and intermediate
		// parents. Both are writable, unnamed records; durable SQLite digests
		// distinguish old final images that lost their tag after a redeploy.
		candidates = append(candidates, imageGarbageCollectCandidate{
			id: image.ID(), digests: imageDigests(image), cachedAt: layer.Created,
			readOnly: image.IsReadOnly(),
			final:    len(image.Names()) > 0 || imageMatchesDigest(image, request.KnownFinalImageDigests),
		})
	}
	eligible := selectImageGarbageCollectCandidates(
		candidates,
		request.FinalImageBefore,
		request.BuildCacheBefore,
		nil,
		nil,
	)
	eligibleIDs := make(map[string]struct{}, len(eligible))
	for _, candidate := range eligible {
		eligibleIDs[candidate.id] = struct{}{}
	}
	for _, image := range images {
		if _, removable := eligibleIDs[image.ID()]; !removable {
			protectedIDs[image.ID()] = struct{}{}
		}
	}
	for imageID := range protectedIDs {
		image := byID[imageID]
		for image != nil {
			parent, parentErr := image.Parent(ctx)
			if parentErr != nil {
				return ImageGarbageCollectResult{}, fmt.Errorf("resolve cached image ancestry for %s: %w", image.ID(), parentErr)
			}
			if parent == nil {
				break
			}
			protectedIDs[parent.ID()] = struct{}{}
			image = parent
		}
	}
	selected := selectImageGarbageCollectCandidates(
		candidates,
		request.FinalImageBefore,
		request.BuildCacheBefore,
		protectedIDs,
		request.ProtectedDigests,
	)
	result := ImageGarbageCollectResult{}
	var failures []error
	for _, candidate := range selected {
		reports, removeErrors := e.runtime.LibimageRuntime().RemoveImages(ctx, []string{candidate.id}, &libimage.RemoveImagesOptions{
			Force: false, Ignore: true, NoPrune: true, WithSize: true,
		})
		if len(removeErrors) > 0 {
			result.Skipped++
			for _, removeErr := range removeErrors {
				failures = append(failures, fmt.Errorf("remove cached image %s: %w", candidate.id, removeErr))
			}
			continue
		}
		for _, report := range reports {
			if !report.Removed {
				continue
			}
			switch candidate.kind {
			case imageGarbageCollectFinal:
				result.FinalImagesRemoved++
			default:
				result.BuildCacheImagesRemoved++
			}
			result.RemovedBytes += max(report.Size, 0)
		}
	}
	return result, errors.Join(failures...)
}

func (e *Engine) garbageCollectOrphanLayers(maximumAge time.Duration) (ImageGarbageCollectResult, error) {
	e.imageOperations.Lock()
	defer e.imageOperations.Unlock()
	return e.garbageCollectOrphanLayersLocked(maximumAge)
}

func (e *Engine) garbageCollectOrphanLayersLocked(maximumAge time.Duration) (ImageGarbageCollectResult, error) {
	report, err := e.store.Check(&storage.CheckOptions{LayerUnreferencedMaximumAge: &maximumAge})
	if err != nil {
		return ImageGarbageCollectResult{}, fmt.Errorf("check container storage for orphan layers: %w", err)
	}
	layers, err := e.store.Layers()
	if err != nil {
		return ImageGarbageCollectResult{}, fmt.Errorf("list container storage layers: %w", err)
	}
	byID := make(map[string]storage.Layer, len(layers))
	for _, layer := range layers {
		byID[layer.ID] = layer
	}
	candidates := make([]orphanLayerCandidate, 0)
	for id, reported := range report.Layers {
		if !onlyUnreferencedLayerErrors(reported) {
			continue
		}
		layer, exists := byID[id]
		if !exists || layer.ReadOnly {
			continue
		}
		candidates = append(candidates, orphanLayerCandidate{
			id: id, size: max(layer.UncompressedSize, 0), created: layer.Created,
		})
	}
	for index := range candidates {
		candidates[index].depth = layerDepth(candidates[index].id, byID)
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].depth != candidates[right].depth {
			return candidates[left].depth > candidates[right].depth
		}
		if !candidates[left].created.Equal(candidates[right].created) {
			return candidates[left].created.After(candidates[right].created)
		}
		return candidates[left].id < candidates[right].id
	})

	result := ImageGarbageCollectResult{}
	var failures []error
	for _, candidate := range candidates {
		if err := e.store.DeleteLayer(candidate.id); err != nil {
			result.Skipped++
			failures = append(failures, fmt.Errorf("delete orphan layer %s: %w", candidate.id, err))
			continue
		}
		result.OrphanLayersRemoved++
		result.RemovedBytes += candidate.size
	}
	return result, errors.Join(failures...)
}

func imageDigests(image *libimage.Image) []string {
	digests := make([]string, 0, len(image.Digests())+1)
	if digest := image.Digest().String(); digest != "" {
		digests = append(digests, digest)
	}
	for _, digest := range image.Digests() {
		digests = append(digests, digest.String())
	}
	return digests
}

func imageMatchesDigest(image *libimage.Image, expected map[string]struct{}) bool {
	if _, exists := expected[image.Digest().String()]; exists {
		return true
	}
	for _, digest := range image.Digests() {
		if _, exists := expected[digest.String()]; exists {
			return true
		}
	}
	return false
}

func onlyUnreferencedLayerErrors(reported []error) bool {
	if len(reported) == 0 {
		return false
	}
	for _, reportedErr := range reported {
		if !errors.Is(reportedErr, storage.ErrLayerUnreferenced) {
			return false
		}
	}
	return true
}

func layerDepth(id string, layers map[string]storage.Layer) int {
	depth := 0
	seen := make(map[string]struct{})
	for id != "" {
		if _, exists := seen[id]; exists {
			break
		}
		seen[id] = struct{}{}
		layer, exists := layers[id]
		if !exists || layer.Parent == "" {
			break
		}
		depth++
		id = layer.Parent
	}
	return depth
}
