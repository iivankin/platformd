package server

import (
	"encoding/json"
	"mime"
	"net/http"
)

func decodeStrictJSONRequest(
	response http.ResponseWriter,
	request *http.Request,
	destination any,
	maximumBytes int64,
	invalidMessage string,
) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", invalidMessage)
		return false
	}
	return true
}
