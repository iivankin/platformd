package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/managedimages"
)

type ManagedImageCatalog interface {
	List(context.Context, managedimages.Engine, int, int) (managedimages.Page, error)
}

func registerManagedImageRoutes(mux *http.ServeMux, catalog ManagedImageCatalog) {
	mux.HandleFunc("GET /api/v1/managed-images/{engine}/tags", getManagedImageTags(catalog))
}

func getManagedImageTags(catalog ManagedImageCatalog) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		page, pageSize, ok := parseManagedImagePage(response, request)
		if !ok {
			return
		}
		result, err := catalog.List(request.Context(), managedimages.Engine(request.PathValue("engine")), page, pageSize)
		if err == nil {
			result, err = managedimages.Filter(result, request.URL.Query().Get("search"))
		}
		switch {
		case err == nil:
			writeJSON(response, http.StatusOK, result)
		case errors.Is(err, managedimages.ErrInvalidQuery):
			writeAPIError(response, http.StatusBadRequest, "invalid_managed_image_query", err.Error())
		default:
			writeAPIError(response, http.StatusBadGateway, "managed_image_catalog_unavailable", "Unable to list official image tags")
		}
	}
}

func parseManagedImagePage(response http.ResponseWriter, request *http.Request) (int, int, bool) {
	page := 0
	pageSize := 0
	values := []struct {
		name        string
		destination *int
	}{{"page", &page}, {"pageSize", &pageSize}}
	for _, value := range values {
		raw := request.URL.Query().Get(value.name)
		if raw == "" {
			continue
		}
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_managed_image_query", value.name+" must be an integer")
			return 0, 0, false
		}
		*value.destination = parsed
	}
	return page, pageSize, true
}
