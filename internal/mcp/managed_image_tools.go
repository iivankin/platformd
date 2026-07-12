package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/iivankin/platformd/internal/managedimages"
)

func (handler *Handler) listManagedImageTags(ctx context.Context, arguments json.RawMessage) (any, error) {
	var input struct {
		Engine   managedimages.Engine `json:"engine"`
		Page     int                  `json:"page"`
		PageSize int                  `json:"pageSize"`
		Search   string               `json:"search"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.Engine == "" || input.Page < 0 || input.PageSize < 0 || input.PageSize > managedimages.MaximumPageSize {
		return nil, fmt.Errorf("%w: engine is required, page must be positive, and pageSize must be 1..100", errInvalidArguments)
	}
	page, err := handler.images.List(ctx, input.Engine, input.Page, input.PageSize)
	if err != nil {
		return nil, err
	}
	return managedimages.Filter(page, input.Search)
}
