package daemon

import (
	"context"
	"errors"
	"strings"

	"github.com/iivankin/platformd/internal/registry"
	"go.podman.io/image/v5/docker/reference"
)

type embeddedImageSourceResolver struct {
	runtime       *runtimeStack
	application   *registry.Application
	generatedRoot string
}

func (resolver embeddedImageSourceResolver) Resolve(ctx context.Context, value string) (string, func(), bool, error) {
	if !resolver.runtime.isEmbeddedReference(value) {
		return "", nil, false, nil
	}
	named, err := reference.ParseDockerRef(strings.TrimSpace(value))
	if err != nil {
		return "", nil, true, err
	}
	repositoryName := reference.Path(named)
	manifestReference := ""
	if digested, ok := named.(reference.Digested); ok {
		manifestReference = digested.Digest().String()
	} else if tagged, ok := named.(reference.Tagged); ok {
		manifestReference = tagged.Tag()
	}
	if repositoryName == "" || manifestReference == "" {
		return "", nil, true, errors.New("embedded registry image must include a tag or digest")
	}
	local, err := resolver.application.PrepareLocalPull(
		ctx, resolver.generatedRoot, repositoryName, manifestReference,
	)
	if err != nil {
		return "", nil, true, err
	}
	return local.Reference, local.Close, true, nil
}
