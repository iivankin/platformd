package automationapi

import "net/http"

func serveOpenAPI(hostname string) http.HandlerFunc {
	document := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title": "platformd Automation API", "version": "v1",
		},
		"servers":  []map[string]string{{"url": "https://" + hostname}},
		"security": []map[string][]string{{"bearerAuth": {}}},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]string{"type": "http", "scheme": "bearer"},
			},
		},
		"paths": map[string]any{
			"/api/v1/me":                                                    readOperation("Read current token identity"),
			"/api/v1/projects":                                              readOperation("List visible projects"),
			"/api/v1/projects/{projectID}":                                  readOperation("Get one visible project"),
			"/api/v1/projects/{projectID}/services":                         readOperation("List services in one visible project"),
			"/api/v1/projects/{projectID}/services/{serviceID}":             readOperation("Get one visible service"),
			"/api/v1/projects/{projectID}/services/{serviceID}/deployments": readOperation("List bounded deployment history"),
		},
	}
	return func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, document)
	}
}

func readOperation(summary string) map[string]any {
	return map[string]any{
		"get": map[string]any{
			"summary": summary,
			"responses": map[string]any{
				"200": map[string]string{"description": "Successful response"},
				"401": map[string]string{"description": "Missing or invalid Bearer token"},
				"403": map[string]string{"description": "Outside token project boundary"},
			},
		},
	}
}
