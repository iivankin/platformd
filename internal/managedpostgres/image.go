package managedpostgres

import (
	"context"
	"fmt"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

func (controller *Controller) resolveImage(ctx context.Context, resource state.ManagedPostgres) (containerengine.Image, error) {
	reference, err := managedimages.Reference(managedimages.PostgreSQL, resource.ImageTag)
	if err != nil {
		return containerengine.Image{}, err
	}
	pinned, err := serviceconfig.PinnedReference(reference, resource.ImageDigest)
	if err != nil {
		return containerengine.Image{}, err
	}
	image, inspectErr := controller.engine.InspectImage(ctx, resource.ImageDigest)
	if inspectErr != nil {
		if err := controller.growth.PermitGrowth(ctx); err != nil {
			return containerengine.Image{}, fmt.Errorf("managed PostgreSQL image is not cached: %w", err)
		}
		image, err = controller.engine.Pull(ctx, containerengine.PullRequest{Reference: pinned})
		if err != nil {
			return containerengine.Image{}, fmt.Errorf("pull pinned managed PostgreSQL image: %w", err)
		}
	}
	if image.ID == "" || image.Digest != resource.ImageDigest {
		return containerengine.Image{}, fmt.Errorf("managed PostgreSQL image digest = %q, want %q", image.Digest, resource.ImageDigest)
	}
	return image, nil
}
