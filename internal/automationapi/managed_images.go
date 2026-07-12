package automationapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/managedimages"
)

func listManagedImageTags(catalog ManagedImageCatalog) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireIdentity(response, request); !ok {
			return
		}
		page, pageSize, ok := managedImagePage(response, request)
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
			writeError(response, http.StatusBadRequest, "invalid_managed_image_query", err.Error())
		default:
			writeError(response, http.StatusBadGateway, "managed_image_catalog_unavailable", "Unable to list official image tags")
		}
	}
}

func managedImagePage(response http.ResponseWriter, request *http.Request) (int, int, bool) {
	page := 0
	pageSize := 0
	values := []struct {
		name        string
		destination *int
	}{{"page", &page}, {"pageSize", &pageSize}}
	for _, entry := range values {
		value := request.URL.Query().Get(entry.name)
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			writeError(response, http.StatusBadRequest, "invalid_managed_image_query", entry.name+" must be an integer")
			return 0, 0, false
		}
		*entry.destination = parsed
	}
	return page, pageSize, true
}
