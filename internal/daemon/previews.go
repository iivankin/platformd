package daemon

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/cloudflaredns"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/preview"
	"github.com/iivankin/platformd/internal/state"
)

func (stack *runtimeStack) ConfigurePreviews(
	ctx context.Context,
	store *state.Store,
	master cryptobox.MasterKey,
	dns *cloudflaredns.Application,
	domains *liveDomainRepository,
	certificateCovers func(string) bool,
	containerLogs containerlogs.Sink,
) error {
	application, err := preview.New(preview.Config{
		Store: store, Engine: stack.engine,
		Environment: resourceVariableResolver{store: store, master: master},
		DNS:         dns, Growth: stack.growth, Admission: stack.admission,
		Placement: stack.previewPlacement, RoutesChanged: domains.reload,
		CertificateCovers: certificateCovers,
		ContainerLogs:     containerLogs,
	})
	if err != nil {
		return err
	}
	stack.mu.Lock()
	if stack.closed {
		stack.mu.Unlock()
		return errors.New("container runtime is closed")
	}
	stack.previews = application
	stack.mu.Unlock()
	if err := application.Restore(ctx); err != nil {
		return err
	}
	go application.RunCleanup(ctx)
	return nil
}

func (stack *runtimeStack) DeployUploadedPreview(
	ctx context.Context,
	serviceID, previewID, tag, revisionID, imageReference string,
	image containerengine.Image,
	identity state.ImageUploadIdentity,
) (string, error) {
	stack.mu.Lock()
	application := stack.previews
	closed := stack.closed
	stack.mu.Unlock()
	if closed || application == nil {
		return "", errors.New("preview runtime is not configured")
	}
	return application.DeployUploaded(ctx, serviceID, previewID, tag, revisionID, imageReference, image, identity)
}

func (stack *runtimeStack) stopServicePreviews(ctx context.Context, serviceID, reason string) error {
	stack.mu.Lock()
	application := stack.previews
	stack.mu.Unlock()
	if application == nil {
		return nil
	}
	return application.StopService(ctx, serviceID, reason)
}

func (stack *runtimeStack) stopAllPreviews(ctx context.Context) error {
	stack.mu.Lock()
	application := stack.previews
	stack.mu.Unlock()
	if application == nil {
		return nil
	}
	return application.StopAll(ctx)
}
