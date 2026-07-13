package registry

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/iivankin/platformd/internal/registryname"
)

func (handler *HTTPHandler) beginUpload(response http.ResponseWriter, request *http.Request, authentication Authentication) {
	if request.Method != http.MethodPost {
		writeDistributionError(response, http.StatusMethodNotAllowed, "UNSUPPORTED", "upload operation is not supported")
		return
	}
	upload, err := handler.application.BeginUpload(request.Context(), authentication)
	if err != nil {
		writeRegistryApplicationError(response, err, "BLOB_UPLOAD_INVALID")
		return
	}
	writeUploadProgress(response, authentication.Repository.Name, upload.ID, 0, http.StatusAccepted)
}

func (handler *HTTPHandler) upload(response http.ResponseWriter, request *http.Request, authentication Authentication, uploadID string) {
	switch request.Method {
	case http.MethodGet:
		_, size, err := handler.application.UploadStatus(request.Context(), authentication, uploadID)
		if err != nil {
			writeRegistryApplicationError(response, err, "BLOB_UPLOAD_INVALID")
			return
		}
		writeUploadProgress(response, authentication.Repository.Name, uploadID, size, http.StatusNoContent)
	case http.MethodPatch:
		if err := handler.validateUploadRange(request, authentication, uploadID); err != nil {
			writeDistributionError(response, http.StatusRequestedRangeNotSatisfiable, "BLOB_UPLOAD_INVALID", err.Error())
			return
		}
		size, err := handler.application.AppendUpload(request.Context(), authentication, uploadID, request.Body)
		if err != nil {
			writeRegistryApplicationError(response, err, "BLOB_UPLOAD_INVALID")
			return
		}
		writeUploadProgress(response, authentication.Repository.Name, uploadID, size, http.StatusAccepted)
	case http.MethodPut:
		digest := request.URL.Query().Get("digest")
		if err := registryname.ValidateDigest(digest); err != nil {
			writeDistributionError(response, http.StatusBadRequest, "DIGEST_INVALID", "blob digest is invalid")
			return
		}
		var finalChunk io.Reader
		if request.ContentLength != 0 {
			finalChunk = request.Body
		}
		_, err := handler.application.FinalizeUpload(request.Context(), authentication, uploadID, digest, finalChunk)
		if err != nil {
			writeRegistryApplicationError(response, err, "BLOB_UPLOAD_INVALID")
			return
		}
		response.Header().Set("Docker-Content-Digest", digest)
		response.Header().Set("Location", blobLocation(authentication.Repository.Name, digest))
		response.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		if err := handler.application.CancelUpload(request.Context(), authentication, uploadID); err != nil {
			writeRegistryApplicationError(response, err, "BLOB_UPLOAD_INVALID")
			return
		}
		response.WriteHeader(http.StatusNoContent)
	default:
		writeDistributionError(response, http.StatusMethodNotAllowed, "UNSUPPORTED", "upload operation is not supported")
	}
}

func (handler *HTTPHandler) validateUploadRange(request *http.Request, authentication Authentication, uploadID string) error {
	value := request.Header.Get("Content-Range")
	if value == "" {
		return nil
	}
	_, size, err := handler.application.UploadStatus(request.Context(), authentication, uploadID)
	if err != nil {
		return err
	}
	start, _, ok := strings.Cut(value, "-")
	if !ok {
		return errors.New("upload Content-Range is invalid")
	}
	parsed, err := strconv.ParseInt(start, 10, 64)
	if err != nil || parsed != size {
		return errors.New("upload Content-Range does not begin at current offset")
	}
	return nil
}

func writeUploadProgress(response http.ResponseWriter, repository, uploadID string, size int64, status int) {
	end := int64(0)
	if size > 0 {
		end = size - 1
	}
	response.Header().Set("Docker-Upload-UUID", uploadID)
	response.Header().Set("Location", "/v2/"+repository+"/blobs/uploads/"+uploadID)
	response.Header().Set("Range", "0-"+strconv.FormatInt(end, 10))
	response.WriteHeader(status)
}
