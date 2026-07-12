//go:build linux && amd64 && cgo

package containerengine

import (
	"context"
	"fmt"

	"go.podman.io/common/libimage"
	commonconfig "go.podman.io/common/pkg/config"
)

func (e *Engine) Pull(ctx context.Context, request PullRequest) (Image, error) {
	if request.Reference == "" {
		return Image{}, fmt.Errorf("image reference is empty")
	}
	policy := commonconfig.PullPolicyMissing
	if request.Refresh {
		policy = commonconfig.PullPolicyAlways
	}

	images, err := e.runtime.LibimageRuntime().Pull(ctx, request.Reference, policy, &libimage.PullOptions{
		CopyOptions: libimage.CopyOptions{
			Username:            request.Username,
			Password:            request.Password,
			SignaturePolicyPath: e.config.SignaturePolicy,
		},
	})
	if err != nil {
		return Image{}, fmt.Errorf("pull image %s: %w", request.Reference, err)
	}
	if len(images) != 1 {
		return Image{}, fmt.Errorf("pull image %s returned %d images", request.Reference, len(images))
	}
	return e.inspectImage(ctx, images[0].ID())
}

func (e *Engine) InspectImage(ctx context.Context, idOrName string) (Image, error) {
	return e.inspectImage(ctx, idOrName)
}

func (e *Engine) inspectImage(ctx context.Context, idOrName string) (Image, error) {
	image, _, err := e.runtime.LibimageRuntime().LookupImage(idOrName, nil)
	if err != nil {
		return Image{}, fmt.Errorf("lookup image %s: %w", idOrName, err)
	}
	data, err := image.Inspect(ctx, &libimage.InspectOptions{WithSize: true})
	if err != nil {
		return Image{}, fmt.Errorf("inspect image %s: %w", idOrName, err)
	}
	result := Image{
		ID:     image.ID(),
		Digest: image.Digest().String(),
		Names:  image.Names(),
		Size:   data.Size,
	}
	if data.Created != nil {
		result.Created = *data.Created
	}
	return result, nil
}
