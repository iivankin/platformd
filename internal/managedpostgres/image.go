package managedpostgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/postgresextension"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

func (controller *Controller) resolveImage(ctx context.Context, resource state.ManagedPostgres) (containerengine.Image, error) {
	var extensions []state.ManagedPostgresExtension
	var err error
	if controller.extensions != nil {
		extensions, err = controller.extensions.ManagedPostgresExtensions(ctx, resource.ID)
		if err != nil {
			return containerengine.Image{}, fmt.Errorf("load managed PostgreSQL extension recipes: %w", err)
		}
	}
	return controller.resolveImageWithExtensions(ctx, resource, extensions, nil)
}

func (controller *Controller) resolveImageWithExtensions(
	ctx context.Context,
	resource state.ManagedPostgres,
	extensions []state.ManagedPostgresExtension,
	progress func(string),
) (containerengine.Image, error) {
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
	if len(extensions) == 0 {
		return image, nil
	}
	if !postgresextension.IsDebianTag(resource.ImageTag) {
		return containerengine.Image{}, errors.New("runtime PostgreSQL extensions require a Debian-based official PostgreSQL image")
	}
	if controller.extensionBuilder == nil {
		return containerengine.Image{}, errors.New("runtime PostgreSQL extension builder is unavailable")
	}
	placement, err := controller.placement(resource)
	if err != nil {
		return containerengine.Image{}, fmt.Errorf("place PostgreSQL extension builder: %w", err)
	}
	derived, err := controller.extensionBuilder.Ensure(ctx, postgresextension.BuildRequest{
		Base: image, Extensions: extensions, ProjectID: resource.ProjectID, PostgresID: resource.ID,
		Network: placement.NetworkName, CgroupParent: placement.CgroupParent, Progress: progress,
	})
	if err != nil {
		return containerengine.Image{}, fmt.Errorf("prepare managed PostgreSQL extension image: %w", err)
	}
	if derived.ID == "" {
		return containerengine.Image{}, errors.New("managed PostgreSQL extension image has no ID")
	}
	return derived, nil
}
