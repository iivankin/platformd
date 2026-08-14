package containerlogs

import (
	"context"
	"errors"
	"io"
)

type AttachEngine interface {
	// StartContainerAttached takes ownership of the writers only when it
	// succeeds. The caller closes both writers after a returned error.
	StartContainerAttached(context.Context, string, io.WriteCloser, io.WriteCloser) (<-chan error, error)
}

type Sink interface {
	ContainerWriter(resourceID, resourceName, deploymentID, attemptID, stream string) io.WriteCloser
}

type RuntimeMetadata struct {
	ResourceID   string
	ResourceName string
	DeploymentID string
	ContainerID  string
}

func StartAttached(
	ctx context.Context,
	engine AttachEngine,
	sink Sink,
	metadata RuntimeMetadata,
) (<-chan error, error) {
	if engine == nil || sink == nil || metadata.ResourceID == "" || metadata.ResourceName == "" ||
		metadata.DeploymentID == "" || metadata.ContainerID == "" {
		return nil, errors.New("attached container log dependencies are incomplete")
	}
	stdout := sink.ContainerWriter(
		metadata.ResourceID, metadata.ResourceName, metadata.DeploymentID, metadata.ContainerID, "stdout",
	)
	stderr := sink.ContainerWriter(
		metadata.ResourceID, metadata.ResourceName, metadata.DeploymentID, metadata.ContainerID, "stderr",
	)
	done, err := engine.StartContainerAttached(ctx, metadata.ContainerID, stdout, stderr)
	if err != nil {
		return nil, errors.Join(err, stdout.Close(), stderr.Close())
	}
	return done, nil
}
