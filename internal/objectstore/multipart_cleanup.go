package objectstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/state"
)

func (application *Application) CleanupExpiredMultipart(ctx context.Context, limit int) (int, error) {
	uploads, err := application.repository.ExpiredMultipartUploads(ctx, application.now().UnixMilli(), limit)
	if err != nil {
		return 0, err
	}
	cleaned := 0
	var failures []error
	for _, upload := range uploads {
		if err := application.payloads.DeleteMultipart(upload.ObjectStoreID, upload.ID); err != nil {
			failures = append(failures, fmt.Errorf("delete multipart payload %s: %w", upload.ID, err))
			continue
		}
		err := application.repository.AbortMultipartUpload(ctx, upload.ObjectStoreID, upload.ID, upload.ObjectKey)
		if err != nil && !errors.Is(err, state.ErrMultipartUploadNotFound) {
			failures = append(failures, fmt.Errorf("delete multipart metadata %s: %w", upload.ID, err))
			continue
		}
		cleaned++
	}
	return cleaned, errors.Join(failures...)
}
