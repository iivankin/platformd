package imageupload

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/state"
)

type uploadResponse struct {
	ID           string `json:"id"`
	Tag          string `json:"tag"`
	Status       string `json:"status"`
	Offset       int64  `json:"offset"`
	Length       int64  `json:"length"`
	DeploymentID string `json:"deploymentId,omitempty"`
	PreviewID    string `json:"previewId,omitempty"`
	Digest       string `json:"digest,omitempty"`
	PreviewURL   string `json:"previewUrl,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

func writeUploadResponse(response http.ResponseWriter, status int, upload state.ImageUpload) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Upload-ID", upload.ID)
	response.Header().Set("Upload-Offset", strconv.FormatInt(upload.ReceivedLength, 10))
	response.Header().Set("Upload-Length", strconv.FormatInt(upload.ExpectedLength, 10))
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(uploadResponse{
		ID: upload.ID, Tag: upload.Tag, Status: upload.Status,
		Offset: upload.ReceivedLength, Length: upload.ExpectedLength,
		DeploymentID: upload.DeploymentID, PreviewID: upload.PreviewID, Digest: upload.ImageDigest,
		PreviewURL: upload.PreviewURL, ErrorCode: upload.ErrorCode, ErrorMessage: upload.ErrorMessage,
	})
}

func writeUploadError(response http.ResponseWriter, status int, code, message string) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
