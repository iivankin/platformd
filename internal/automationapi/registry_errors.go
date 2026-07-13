package automationapi

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/registry"
	"github.com/iivankin/platformd/internal/state"
)

func writeRegistryError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, state.ErrRegistryRepositoryNotFound):
		writeError(response, http.StatusNotFound, "registry_repository_not_found", "Registry repository not found")
	case errors.Is(err, state.ErrRegistryNameConflict):
		writeError(response, http.StatusConflict, "registry_name_conflict", "Registry repository name already exists")
	case errors.Is(err, state.ErrRegistryCredentialNameConflict):
		writeError(response, http.StatusConflict, "registry_credential_name_conflict", "Registry credential name already exists")
	case errors.Is(err, state.ErrRegistryCredentialNotFound):
		writeError(response, http.StatusNotFound, "registry_credential_not_found", "Registry credential not found")
	case errors.Is(err, state.ErrHostnameInUse):
		writeError(response, http.StatusConflict, "hostname_in_use", "Hostname is already assigned to another public role")
	case errors.Is(err, state.ErrCertificateCoverage):
		writeError(response, http.StatusUnprocessableEntity, "certificate_coverage", "No configured Origin certificate covers this hostname")
	case errors.Is(err, registry.ErrInvalidInput):
		writeError(response, http.StatusBadRequest, "invalid_registry_input", err.Error())
	case errors.Is(err, registry.ErrRepositoryBusy):
		writeError(response, http.StatusConflict, "repository_busy", "Registry repository is busy")
	case errors.Is(err, state.ErrRegistryManifestNotFound):
		writeError(response, http.StatusNotFound, "registry_manifest_not_found", "Registry manifest or tag not found")
	default:
		var referenced *registry.ManifestReferencedError
		if errors.As(err, &referenced) {
			writeJSON(response, http.StatusConflict, map[string]any{"error": map[string]any{
				"code": "manifest_referenced", "message": err.Error(), "parentDigests": referenced.Parents,
			}})
		} else {
			writeError(response, http.StatusInternalServerError, "internal_error", "Unable to complete the Registry request")
		}
	}
	return true
}
