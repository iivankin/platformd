package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/iivankin/platformd/internal/registryname"
	"github.com/iivankin/platformd/internal/state"
)

func (handler *HTTPHandler) blob(response http.ResponseWriter, request *http.Request, repository state.RegistryRepository, digest string) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writeDistributionError(response, http.StatusMethodNotAllowed, "UNSUPPORTED", "blob operation is not supported")
		return
	}
	if err := registryname.ValidateDigest(digest); err != nil {
		writeDistributionError(response, http.StatusBadRequest, "DIGEST_INVALID", "blob digest is invalid")
		return
	}
	file, size, err := handler.application.OpenBlob(repository.ID, digest)
	if err != nil {
		writeDistributionError(response, http.StatusNotFound, "BLOB_UNKNOWN", "blob is unknown to this repository")
		return
	}
	defer file.Close()
	offset, length, partial, err := parseRegistryRange(request.Header.Get("Range"), size)
	if err != nil {
		response.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		writeDistributionError(response, http.StatusRequestedRangeNotSatisfiable, "RANGE_INVALID", "blob range is invalid")
		return
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		writeDistributionError(response, http.StatusInternalServerError, "UNKNOWN", "blob seek failed")
		return
	}
	setRegistryCache(response, repository.PublicPull, true)
	response.Header().Set("Accept-Ranges", "bytes")
	response.Header().Set("Content-Type", "application/octet-stream")
	response.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	response.Header().Set("Docker-Content-Digest", digest)
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
		response.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+length-1, size))
	}
	response.WriteHeader(status)
	if request.Method == http.MethodHead || length == 0 {
		return
	}
	_, _ = io.CopyN(response, file, length)
}

func (handler *HTTPHandler) manifest(response http.ResponseWriter, request *http.Request, authentication Authentication, reference string) {
	switch request.Method {
	case http.MethodGet, http.MethodHead:
		manifest, err := handler.application.Manifest(request.Context(), authentication.Repository.ID, reference)
		if err != nil {
			writeDistributionError(response, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest is unknown")
			return
		}
		immutable := registryname.ValidateDigest(reference) == nil
		setRegistryCache(response, authentication.Repository.PublicPull, immutable)
		response.Header().Set("Content-Type", manifest.MediaType)
		response.Header().Set("Content-Length", strconv.Itoa(len(manifest.Body)))
		response.Header().Set("Docker-Content-Digest", manifest.Digest)
		response.WriteHeader(http.StatusOK)
		if request.Method == http.MethodGet {
			_, _ = response.Write(manifest.Body)
		}
	case http.MethodPut:
		request.Body = http.MaxBytesReader(response, request.Body, MaximumManifestSize+1)
		body, err := io.ReadAll(request.Body)
		if err != nil || len(body) > MaximumManifestSize {
			writeDistributionError(response, http.StatusRequestEntityTooLarge, "MANIFEST_INVALID", "manifest exceeds 4 MiB")
			return
		}
		manifest, err := handler.application.PutManifest(request.Context(), authentication, reference, request.Header.Get("Content-Type"), body)
		if err != nil {
			writeRegistryApplicationError(response, err, "MANIFEST_INVALID")
			return
		}
		response.Header().Set("Docker-Content-Digest", manifest.Digest)
		response.Header().Set("Location", manifestLocation(authentication.Repository.Name, manifest.Digest))
		response.WriteHeader(http.StatusCreated)
	default:
		writeDistributionError(response, http.StatusMethodNotAllowed, "UNSUPPORTED", "manifest operation is not supported")
	}
}

func (handler *HTTPHandler) tags(response http.ResponseWriter, request *http.Request, repository state.RegistryRepository) {
	if request.Method != http.MethodGet {
		writeDistributionError(response, http.StatusMethodNotAllowed, "UNSUPPORTED", "tag operation is not supported")
		return
	}
	limit, err := positiveQueryInteger(request, "n", 100, 1000)
	if err != nil {
		writeDistributionError(response, http.StatusBadRequest, "PAGINATION_NUMBER_INVALID", "tag page size is invalid")
		return
	}
	after := request.URL.Query().Get("last")
	if after != "" && registryname.ValidateTag(after) != nil {
		writeDistributionError(response, http.StatusBadRequest, "TAG_INVALID", "tag cursor is invalid")
		return
	}
	tags, more, err := handler.application.Tags(request.Context(), repository.ID, after, limit)
	if err != nil {
		writeDistributionError(response, http.StatusInternalServerError, "UNKNOWN", "unable to list tags")
		return
	}
	names := make([]string, len(tags))
	for index, tag := range tags {
		names[index] = tag.Name
	}
	if more && len(names) != 0 {
		next := "/v2/" + repository.Name + "/tags/list?n=" + strconv.Itoa(limit) + "&last=" + url.QueryEscape(names[len(names)-1])
		response.Header().Set("Link", "<"+next+">; rel=\"next\"")
	}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(map[string]any{"name": repository.Name, "tags": names})
}

func manifestLocation(repository, digest string) string {
	return "/v2/" + repository + "/manifests/" + digest
}

func blobLocation(repository, digest string) string {
	return "/v2/" + repository + "/blobs/" + digest
}

func parseRegistryRange(value string, size int64) (offset, length int64, partial bool, err error) {
	if value == "" {
		return 0, size, false, nil
	}
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") || size == 0 {
		return 0, 0, false, errors.New("unsupported range")
	}
	startValue, endValue, ok := strings.Cut(strings.TrimPrefix(value, "bytes="), "-")
	if !ok {
		return 0, 0, false, errors.New("invalid range")
	}
	if startValue == "" {
		suffix, parseErr := strconv.ParseInt(endValue, 10, 64)
		if parseErr != nil || suffix <= 0 {
			return 0, 0, false, errors.New("invalid suffix range")
		}
		suffix = min(suffix, size)
		return size - suffix, suffix, true, nil
	}
	start, parseErr := strconv.ParseInt(startValue, 10, 64)
	if parseErr != nil || start < 0 || start >= size {
		return 0, 0, false, errors.New("invalid range start")
	}
	end := size - 1
	if endValue != "" {
		end, parseErr = strconv.ParseInt(endValue, 10, 64)
		if parseErr != nil || end < start {
			return 0, 0, false, errors.New("invalid range end")
		}
		end = min(end, size-1)
	}
	return start, end - start + 1, true, nil
}

func writeRegistryApplicationError(response http.ResponseWriter, err error, fallbackCode string) {
	switch {
	case errors.Is(err, ErrBlobDigestMismatch):
		writeDistributionError(response, http.StatusBadRequest, "DIGEST_INVALID", err.Error())
	case errors.Is(err, ErrUploadQuota), errors.Is(err, ErrManifestQuota):
		writeDistributionError(response, http.StatusTooManyRequests, "TOOMANYREQUESTS", err.Error())
	case errors.Is(err, ErrDenied):
		writeDistributionError(response, http.StatusForbidden, "DENIED", err.Error())
	case errors.Is(err, ErrInvalidInput):
		writeDistributionError(response, http.StatusBadRequest, fallbackCode, err.Error())
	case errors.Is(err, state.ErrRegistryUploadNotFound), errors.Is(err, os.ErrNotExist):
		writeDistributionError(response, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "blob upload is unknown")
	default:
		writeDistributionError(response, http.StatusInternalServerError, "UNKNOWN", "registry operation failed")
	}
}
