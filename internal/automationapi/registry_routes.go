package automationapi

import "net/http"

func registerRegistryRoutes(mux *http.ServeMux, application registryApplication, settings registrySettings) {
	mux.HandleFunc("GET /api/v1/registry", getRegistrySettings(settings))
	mux.HandleFunc("PUT /api/v1/registry/hostname", setRegistryHostname(settings))
	mux.HandleFunc("GET /api/v1/registry/repositories", listRegistryRepositories(application))
	mux.HandleFunc("POST /api/v1/registry/repositories", createRegistryRepository(application))
	mux.HandleFunc("GET /api/v1/registry/repositories/{repositoryID}", getRegistryRepository(application))
	mux.HandleFunc("PUT /api/v1/registry/repositories/{repositoryID}/public-pull", setRegistryRepositoryPublicPull(application))
	mux.HandleFunc("GET /api/v1/registry/repositories/{repositoryID}/images", listRegistryImages(application))
	mux.HandleFunc("GET /api/v1/registry/repositories/{repositoryID}/images/{digest}", getRegistryImage(application))
	mux.HandleFunc("DELETE /api/v1/registry/repositories/{repositoryID}/tags/{tag}", deleteRegistryTag(application))
	mux.HandleFunc("DELETE /api/v1/registry/repositories/{repositoryID}/manifests/{digest}", deleteRegistryManifest(application))
	mux.HandleFunc("DELETE /api/v1/registry/repositories/{repositoryID}", deleteRegistryRepository(application))
	mux.HandleFunc("GET /api/v1/registry/repositories/{repositoryID}/credentials", listRegistryCredentials(application))
	mux.HandleFunc("POST /api/v1/registry/repositories/{repositoryID}/credentials", createRegistryCredential(application))
	mux.HandleFunc("DELETE /api/v1/registry/repositories/{repositoryID}/credentials/{credentialID}", deleteRegistryCredential(application))
	mux.HandleFunc("POST /api/v1/registry/repositories/{repositoryID}/cleanup", cleanupRegistryRepository(application))
}
