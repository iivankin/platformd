package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

const MaximumCleanupPreviewDigests = 1000
const RegistryOrphanBlobGrace = 24 * time.Hour

type CleanupResult struct {
	BlobCount        int
	Bytes            int64
	PreviewDigests   []string
	PreviewTruncated bool
	Deleted          bool
	RequestID        string
}

func (application *Application) Cleanup(ctx context.Context, repositoryID string, dryRun bool, actor Actor) (CleanupResult, error) {
	if _, err := application.store.RegistryRepository(ctx, repositoryID); err != nil {
		return CleanupResult{}, err
	}
	if !dryRun {
		if actor.ID == "" || (actor.Kind != "access" && actor.Kind != "token") || (actor.Kind == "access" && actor.Email == "") {
			return CleanupResult{}, fmt.Errorf("%w: cleanup actor is incomplete", ErrInvalidInput)
		}
	}
	releaseMaintenance, err := application.beginRepositoryMaintenance(repositoryID, "cleanup")
	if err != nil {
		return CleanupResult{}, err
	}
	defer releaseMaintenance()
	lock := application.acquireRepositoryLock(repositoryID)
	defer application.releaseRepositoryLock(repositoryID, lock)
	candidates, err := application.cleanupCandidates(ctx, repositoryID, application.now())
	if err != nil {
		return CleanupResult{}, err
	}
	result := CleanupResult{Deleted: !dryRun, BlobCount: len(candidates)}
	for _, candidate := range candidates {
		result.Bytes += candidate.Size
	}
	previewCount := min(len(candidates), MaximumCleanupPreviewDigests)
	result.PreviewDigests = make([]string, previewCount)
	for index := range previewCount {
		result.PreviewDigests[index] = candidates[index].Digest
	}
	result.PreviewTruncated = previewCount != len(candidates)
	if dryRun {
		return result, nil
	}
	for _, candidate := range candidates {
		if err := application.payloads.DeleteBlob(repositoryID, candidate.Digest); err != nil {
			return CleanupResult{}, err
		}
	}
	now := application.now()
	identifiers, err := application.identifiers(now, 2)
	if err != nil {
		return CleanupResult{}, err
	}
	if err := application.store.RecordRegistryCleanup(ctx, state.RegistryCleanupAudit{
		RepositoryID: repositoryID, DeletedBlobCount: result.BlobCount, DeletedBytes: result.Bytes,
		AuditEventID: identifiers[0], ActorKind: actor.Kind, ActorID: actor.ID, ActorEmail: actor.Email,
		RequestCorrelationID: identifiers[1], CreatedAtMillis: now.UnixMilli(),
	}); err != nil {
		return CleanupResult{}, err
	}
	result.RequestID = identifiers[1]
	return result, nil
}

func (application *Application) cleanupCandidates(ctx context.Context, repositoryID string, now time.Time) ([]BlobInfo, error) {
	referenced := make(map[string]struct{})
	after := ""
	for {
		manifests, more, err := application.store.RegistryManifests(ctx, repositoryID, after, 1000)
		if err != nil {
			return nil, err
		}
		for _, manifest := range manifests {
			if manifest.MediaType == OCIImageIndexMediaType || manifest.MediaType == DockerManifestListMediaType {
				continue
			}
			var document manifestDocument
			if err := json.Unmarshal(manifest.Body, &document); err != nil {
				return nil, fmt.Errorf("decode stored manifest during cleanup: %w", err)
			}
			referenced[document.Config.Digest] = struct{}{}
			for _, layer := range document.Layers {
				referenced[layer.Digest] = struct{}{}
			}
		}
		if !more || len(manifests) == 0 {
			break
		}
		after = manifests[len(manifests)-1].Digest
	}
	blobs, err := application.payloads.RepositoryBlobs(repositoryID)
	if err != nil {
		return nil, err
	}
	candidates := make([]BlobInfo, 0)
	for _, blob := range blobs {
		if _, exists := referenced[blob.Digest]; !exists && !blob.ModifiedAt.Add(RegistryOrphanBlobGrace).After(now) {
			candidates = append(candidates, blob)
		}
	}
	slices.SortFunc(candidates, func(left, right BlobInfo) int {
		if left.Digest < right.Digest {
			return -1
		}
		if left.Digest > right.Digest {
			return 1
		}
		return 0
	})
	return candidates, nil
}
