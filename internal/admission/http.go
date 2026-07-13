package admission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

func WrapHTTPMutations(gate *Gate, kind string, skipPath string, next http.Handler) http.Handler {
	if gate == nil || next == nil || !validField(kind) {
		panic("admission HTTP middleware configuration is invalid")
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !mutationMethod(request.Method) || request.URL.Path == skipPath {
			next.ServeHTTP(response, request)
			return
		}
		lease, err := gate.Begin(kind, requestBlockerID(request))
		if err != nil {
			writeUpdating(response)
			return
		}
		defer lease.Release()
		next.ServeHTTP(response, request)
	})
}

func mutationMethod(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func requestBlockerID(request *http.Request) string {
	value := request.Method + " " + request.URL.Path
	if len(value) <= maximumFieldLen {
		return value
	}
	hash := sha256.Sum256([]byte(value))
	return request.Method + " sha256:" + hex.EncodeToString(hash[:])
}

func writeUpdating(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(response).Encode(map[string]any{
		"error": map[string]string{
			"code": "platform_updating", "message": "Platform update is in progress",
		},
	})
}
