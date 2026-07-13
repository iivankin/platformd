package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/state"
)

const (
	maximumObjectStoreCreateBytes = 32 << 10
	maximumObjectPreviewBytes     = 256 << 10
)

type objectStoreResponse struct {
	ID                   string   `json:"id"`
	ProjectID            string   `json:"projectId"`
	Name                 string   `json:"name"`
	BucketName           string   `json:"bucketName"`
	InternalHostname     string   `json:"internalHostname"`
	PublicHostname       string   `json:"publicHostname,omitempty"`
	CORSOrigins          []string `json:"corsOrigins"`
	BackupEnabled        bool     `json:"backupEnabled"`
	BackupCron           string   `json:"backupCron,omitempty"`
	BackupRetentionCount int      `json:"backupRetentionCount"`
	AccessKey            string   `json:"accessKey,omitempty"`
	Secret               string   `json:"secret,omitempty"`
	CredentialPermission string   `json:"credentialPermission,omitempty"`
	Region               string   `json:"region"`
	CreatedAt            int64    `json:"createdAt"`
	UpdatedAt            int64    `json:"updatedAt"`
}

type objectMetadataResponse struct {
	ObjectKey   string `json:"objectKey"`
	ContentType string `json:"contentType,omitempty"`
	ETag        string `json:"etag"`
	Size        int64  `json:"size"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

func registerObjectStoreRoutes(mux *http.ServeMux, application *objectstore.Application) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/object-stores", listObjectStores(application))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/object-stores", createObjectStore(application))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/object-stores/{storeID}", getObjectStore(application))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/object-stores/{storeID}/objects", browseObjects(application))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/object-stores/{storeID}/objects", uploadObject(application))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/object-stores/{storeID}/objects", deleteBrowserObject(application))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/object-stores/{storeID}/objects/preview", previewObject(application))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/object-stores/{storeID}/objects/download", downloadObject(application))
	mux.HandleFunc("HEAD /api/v1/projects/{projectID}/object-stores/{storeID}/objects/download", downloadObject(application))
}

func listObjectStores(application *objectstore.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		stores, err := application.Stores(request.Context(), request.PathValue("projectID"))
		if err != nil {
			writeObjectStoreError(response, err)
			return
		}
		result := make([]objectStoreResponse, 0, len(stores))
		for _, store := range stores {
			result = append(result, publicObjectStore(store, objectstore.CreateResult{}))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getObjectStore(application *objectstore.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		store, err := application.Store(request.Context(), request.PathValue("projectID"), request.PathValue("storeID"))
		if err != nil {
			writeObjectStoreError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicObjectStore(store, objectstore.CreateResult{}))
	}
}

func createObjectStore(application *objectstore.Application) http.HandlerFunc {
	type requestBody struct {
		Name                 string   `json:"name"`
		BucketName           string   `json:"bucketName"`
		PublicHostname       string   `json:"publicHostname"`
		CORSOrigins          []string `json:"corsOrigins"`
		CredentialName       string   `json:"credentialName"`
		CredentialPermission string   `json:"credentialPermission"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumObjectStoreCreateBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid object store fields")
			return
		}
		result, err := application.Create(request.Context(), objectstore.CreateInput{
			ProjectID: request.PathValue("projectID"), Name: body.Name, BucketName: body.BucketName,
			PublicHostname: body.PublicHostname, CORSOrigins: body.CORSOrigins,
			CredentialName: body.CredentialName, CredentialPermission: body.CredentialPermission,
			Actor: objectstore.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if err != nil {
			writeObjectStoreError(response, err)
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+result.Store.ProjectID+"/object-stores/"+result.Store.ID)
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicObjectStore(result.Store, result))
	}
}

func browseObjects(application *objectstore.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		store, err := application.Store(request.Context(), request.PathValue("projectID"), request.PathValue("storeID"))
		if err != nil {
			writeObjectStoreError(response, err)
			return
		}
		limit := 100
		if value := request.URL.Query().Get("limit"); value != "" {
			limit, err = strconv.Atoi(value)
			if err != nil || limit < 1 || limit > 1000 {
				writeAPIError(response, http.StatusBadRequest, "invalid_object_query", "limit must be 1..1000")
				return
			}
		}
		after := ""
		if token := request.URL.Query().Get("continuationToken"); token != "" {
			after, err = application.DecodeContinuationToken(store.ID, token)
			if err != nil {
				writeAPIError(response, http.StatusBadRequest, "invalid_object_query", "continuation token is invalid")
				return
			}
		}
		objects, more, err := application.List(request.Context(), store.ID, request.URL.Query().Get("prefix"), after, limit)
		if err != nil {
			writeObjectStoreError(response, err)
			return
		}
		nextToken := ""
		if more && len(objects) != 0 {
			nextToken, err = application.EncodeContinuationToken(store.ID, objects[len(objects)-1].ObjectKey)
			if err != nil {
				writeObjectStoreError(response, err)
				return
			}
		}
		result := make([]objectMetadataResponse, 0, len(objects))
		for _, object := range objects {
			result = append(result, publicObjectMetadata(object))
		}
		writeJSON(response, http.StatusOK, map[string]any{"objects": result, "nextContinuationToken": nextToken})
	}
}

func uploadObject(application *objectstore.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		store, err := application.Store(request.Context(), request.PathValue("projectID"), request.PathValue("storeID"))
		if err != nil {
			writeObjectStoreError(response, err)
			return
		}
		if request.ContentLength > objectstore.MaximumObjectSize {
			writeAPIError(response, http.StatusRequestEntityTooLarge, "object_too_large", "Object exceeds 100 GiB")
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, objectstore.MaximumObjectSize+1)
		created, err := application.Put(request.Context(), objectstore.PutInput{
			StoreID: store.ID, ObjectKey: request.URL.Query().Get("key"),
			ContentType: request.Header.Get("Content-Type"), Body: request.Body,
		})
		if err != nil {
			writeObjectStoreError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, publicObjectMetadata(created))
	}
}

func deleteBrowserObject(application *objectstore.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		store, err := application.Store(request.Context(), request.PathValue("projectID"), request.PathValue("storeID"))
		if err != nil {
			writeObjectStoreError(response, err)
			return
		}
		if err := application.Delete(request.Context(), store.ID, request.URL.Query().Get("key")); err != nil {
			writeObjectStoreError(response, err)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}
}

func previewObject(application *objectstore.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		store, err := application.Store(request.Context(), request.PathValue("projectID"), request.PathValue("storeID"))
		if err != nil {
			writeObjectStoreError(response, err)
			return
		}
		object, err := application.Object(request.Context(), store.ID, request.URL.Query().Get("key"))
		if err != nil {
			writeObjectStoreError(response, err)
			return
		}
		if object.Metadata.Size > maximumObjectPreviewBytes || !previewContentType(object.Metadata.ContentType) || secretLikeObjectKey(object.Metadata.ObjectKey) {
			writeJSON(response, http.StatusOK, map[string]any{"allowed": false, "metadata": publicObjectMetadata(object.Metadata)})
			return
		}
		var output bytes.Buffer
		if err := application.ReadRange(request.Context(), object, 0, object.Metadata.Size, &output); err != nil {
			writeObjectStoreError(response, err)
			return
		}
		result := map[string]any{
			"allowed": true, "metadata": publicObjectMetadata(object.Metadata),
			"base64": base64.RawURLEncoding.EncodeToString(output.Bytes()),
		}
		if strings.HasPrefix(object.Metadata.ContentType, "text/") && utf8.Valid(output.Bytes()) {
			result["text"] = output.String()
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func publicObjectMetadata(object state.ObjectMetadata) objectMetadataResponse {
	return objectMetadataResponse{
		ObjectKey: object.ObjectKey, ContentType: object.ContentType, ETag: object.ETag,
		Size: object.Size, CreatedAt: object.CreatedAtMillis, UpdatedAt: object.UpdatedAtMillis,
	}
}

func downloadObject(application *objectstore.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		store, err := application.Store(request.Context(), request.PathValue("projectID"), request.PathValue("storeID"))
		if err != nil {
			writeObjectStoreError(response, err)
			return
		}
		object, err := application.Object(request.Context(), store.ID, request.URL.Query().Get("key"))
		if err != nil {
			writeObjectStoreError(response, err)
			return
		}
		response.Header().Set("Content-Type", object.Metadata.ContentType)
		response.Header().Set("Content-Length", strconv.FormatInt(object.Metadata.Size, 10))
		response.Header().Set("Content-Disposition", "attachment")
		if request.Method == http.MethodHead {
			return
		}
		_ = application.ReadRange(request.Context(), object, 0, object.Metadata.Size, response)
	}
}

func publicObjectStore(store state.ObjectStore, created objectstore.CreateResult) objectStoreResponse {
	return objectStoreResponse{
		ID: store.ID, ProjectID: store.ProjectID, Name: store.Name, BucketName: store.BucketName,
		InternalHostname: store.Name + "." + store.ProjectName + ".internal", PublicHostname: store.PublicHostname,
		CORSOrigins: store.CORSOrigins, BackupEnabled: store.BackupEnabled, BackupCron: store.BackupCron,
		BackupRetentionCount: store.BackupRetentionCount, AccessKey: created.AccessKey, Secret: created.Secret,
		CredentialPermission: created.Credential.Permission, CreatedAt: store.CreatedAtMillis, UpdatedAt: store.UpdatedAtMillis,
		Region: objectstore.Region,
	}
}

func previewContentType(value string) bool {
	return strings.HasPrefix(value, "text/") || strings.HasPrefix(value, "image/")
}

func secretLikeObjectKey(value string) bool {
	value = strings.ToLower(value)
	for _, marker := range []string{".env", "secret", "password", "credential", "private-key", "token"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func writeObjectStoreError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrProjectNotFound):
		writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrObjectStoreNotFound):
		writeAPIError(response, http.StatusNotFound, "object_store_not_found", "Object store not found")
	case errors.Is(err, state.ErrObjectNotFound):
		writeAPIError(response, http.StatusNotFound, "object_not_found", "Object not found")
	case errors.Is(err, state.ErrResourceNameConflict):
		writeAPIError(response, http.StatusConflict, "resource_name_conflict", "A project resource with this name already exists")
	case errors.Is(err, state.ErrHostnameInUse):
		writeAPIError(response, http.StatusConflict, "hostname_in_use", "Hostname is already assigned to another public role")
	case errors.Is(err, state.ErrCertificateCoverage):
		writeAPIError(response, http.StatusUnprocessableEntity, "certificate_coverage", "No configured Origin certificate covers this hostname")
	case errors.Is(err, objectstore.ErrInvalidInput):
		writeAPIError(response, http.StatusBadRequest, "invalid_object_store", err.Error())
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to manage object store")
	}
}
