package automationapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/state"
)

const objectStoreRegion = "us-east-1"

type objectStoreApplication interface {
	Stores(context.Context, string) ([]state.ObjectStore, error)
	Store(context.Context, string, string) (state.ObjectStore, error)
	Create(context.Context, objectstore.CreateInput) (objectstore.CreateResult, error)
}

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

func listObjectStores(application objectStoreApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if !requireProject(response, request, projectID) {
			return
		}
		stores, err := application.Stores(request.Context(), projectID)
		if err != nil {
			writeRepositoryError(response, err)
			return
		}
		result := make([]objectStoreResponse, 0, len(stores))
		for _, store := range stores {
			result = append(result, publicObjectStore(store, objectstore.CreateResult{}))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getObjectStore(application objectStoreApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if !requireProject(response, request, projectID) {
			return
		}
		store, err := application.Store(request.Context(), projectID, request.PathValue("storeID"))
		if err != nil {
			writeRepositoryError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicObjectStore(store, objectstore.CreateResult{}))
	}
}

func createObjectStore(application objectStoreApplication) http.HandlerFunc {
	type requestBody struct {
		Name                 string   `json:"name"`
		BucketName           string   `json:"bucketName"`
		PublicHostname       string   `json:"publicHostname"`
		CORSOrigins          []string `json:"corsOrigins"`
		CredentialName       string   `json:"credentialName"`
		CredentialPermission string   `json:"credentialPermission"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		identity, ok := requireAdminProject(response, request, projectID)
		if !ok {
			return
		}
		var body requestBody
		if !decodeMutationJSON(response, request, &body) {
			return
		}
		result, err := application.Create(request.Context(), objectstore.CreateInput{
			ProjectID: projectID, Name: body.Name, BucketName: body.BucketName,
			PublicHostname: body.PublicHostname, CORSOrigins: body.CORSOrigins,
			CredentialName: body.CredentialName, CredentialPermission: body.CredentialPermission,
			Actor: objectstore.Actor{Kind: "token", ID: identity.TokenID},
		})
		if err != nil {
			writeObjectStoreMutationError(response, err)
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+result.Store.ProjectID+"/object-stores/"+result.Store.ID)
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicObjectStore(result.Store, result))
	}
}

func publicObjectStore(store state.ObjectStore, created objectstore.CreateResult) objectStoreResponse {
	return objectStoreResponse{
		ID: store.ID, ProjectID: store.ProjectID, Name: store.Name, BucketName: store.BucketName,
		InternalHostname: store.Name + "." + store.ProjectName + ".internal",
		PublicHostname:   store.PublicHostname, CORSOrigins: append([]string(nil), store.CORSOrigins...),
		BackupEnabled: store.BackupEnabled, BackupCron: store.BackupCron,
		BackupRetentionCount: store.BackupRetentionCount,
		AccessKey:            created.AccessKey, Secret: created.Secret,
		CredentialPermission: created.Credential.Permission,
		Region:               objectStoreRegion, CreatedAt: store.CreatedAtMillis, UpdatedAt: store.UpdatedAtMillis,
	}
}

func writeObjectStoreMutationError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, objectstore.ErrInvalidInput):
		writeError(response, http.StatusBadRequest, "invalid_object_store", err.Error())
	case errors.Is(err, state.ErrProjectNotFound):
		writeError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrResourceNameConflict):
		writeError(response, http.StatusConflict, "resource_name_conflict", "A project resource with this name already exists")
	case errors.Is(err, state.ErrHostnameInUse):
		writeError(response, http.StatusConflict, "hostname_in_use", "Hostname is already assigned to another public role")
	case errors.Is(err, state.ErrCertificateCoverage):
		writeError(response, http.StatusUnprocessableEntity, "certificate_coverage", "No configured Origin certificate covers this hostname")
	default:
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to create object store")
	}
}
