package automation

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/containerengine"
)

type ImageGarbageCollector interface {
	ForceGarbageCollect(context.Context) (containerengine.ImageGarbageCollectResult, error)
}

type ImageGCApplication struct {
	collector ImageGarbageCollector
}

func NewImageGCApplication(collector ImageGarbageCollector) (*ImageGCApplication, error) {
	if collector == nil {
		return nil, errors.New("image garbage collector is required")
	}
	return &ImageGCApplication{collector: collector}, nil
}

func (application *ImageGCApplication) Force(ctx context.Context, identity Identity) (containerengine.ImageGarbageCollectResult, error) {
	if identity.TokenID == "" || !identity.IsAdmin() {
		return containerengine.ImageGarbageCollectResult{}, ErrAdminRequired
	}
	return application.collector.ForceGarbageCollect(ctx)
}
