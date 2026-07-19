//go:build linux && amd64 && cgo

package containerengine

import (
	"context"
	"errors"
	"fmt"

	"go.podman.io/common/libimage"
)

func (e *Engine) GarbageCollectImages(ctx context.Context, request ImageGarbageCollectRequest) (ImageGarbageCollectResult, error) {
	if request.Before.IsZero() {
		return ImageGarbageCollectResult{}, errors.New("image garbage collection cutoff is empty")
	}
	images, err := e.runtime.LibimageRuntime().ListImages(ctx, nil)
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
		if imageMatchesProtectedDigest(image, request.ProtectedDigests) {
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
		digests := make([]string, 0, len(image.Digests())+1)
		if digest := image.Digest().String(); digest != "" {
			digests = append(digests, digest)
		}
		for _, digest := range image.Digests() {
			digests = append(digests, digest.String())
		}
		candidates = append(candidates, imageGarbageCollectCandidate{
			id: image.ID(), digests: digests, cachedAt: layer.Created, readOnly: image.IsReadOnly(),
		})
	}
	selected := selectImageGarbageCollectCandidates(candidates, request.Before, protectedIDs, request.ProtectedDigests)
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
			if report.Removed {
				result.Removed++
				result.RemovedBytes += report.Size
			}
		}
	}
	return result, errors.Join(failures...)
}

func imageMatchesProtectedDigest(image *libimage.Image, protected map[string]struct{}) bool {
	if _, exists := protected[image.Digest().String()]; exists {
		return true
	}
	for _, digest := range image.Digests() {
		if _, exists := protected[digest.String()]; exists {
			return true
		}
	}
	return false
}
