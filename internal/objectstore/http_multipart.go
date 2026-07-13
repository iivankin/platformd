package objectstore

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

func (handler *HTTPHandler) createMultipart(response http.ResponseWriter, request *http.Request, store state.ObjectStore, objectKey, requestID string) {
	if len(request.URL.Query()) != 1 {
		writeS3Error(response, http.StatusBadRequest, "InvalidArgument", "Multipart create query is invalid", requestID)
		return
	}
	result, err := handler.application.CreateMultipart(request.Context(), store.ID, objectKey, request.Header.Get("Content-Type"))
	if err != nil {
		writeMultipartError(response, err, requestID)
		return
	}
	writeS3XML(response, http.StatusOK, createMultipartUploadResult{
		XMLNS: "http://s3.amazonaws.com/doc/2006-03-01/", Bucket: store.BucketName,
		Key: objectKey, UploadID: result.Upload.ID,
	})
}

func (handler *HTTPHandler) uploadPart(response http.ResponseWriter, request *http.Request, store state.ObjectStore, objectKey, requestID string) {
	query := request.URL.Query()
	if len(query) != 2 || request.ContentLength > MaximumMultipartPartSize {
		writeS3Error(response, http.StatusRequestEntityTooLarge, "EntityTooLarge", "Multipart part exceeds 512 MiB or query is invalid", requestID)
		return
	}
	partNumber, err := strconv.Atoi(query.Get("partNumber"))
	if err != nil || partNumber < 1 || partNumber > 10_000 {
		writeS3Error(response, http.StatusBadRequest, "InvalidArgument", "partNumber must be 1..10000", requestID)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, MaximumMultipartPartSize+1)
	expected := request.Header.Get("X-Amz-Content-Sha256")
	if expected == "UNSIGNED-PAYLOAD" {
		expected = ""
	}
	part, err := handler.application.UploadPart(request.Context(), store.ID, query.Get("uploadId"), objectKey, partNumber, expected, request.Body)
	if err != nil {
		writeMultipartError(response, err, requestID)
		return
	}
	response.Header().Set("ETag", `"`+part.ChecksumSHA256+`"`)
	response.WriteHeader(http.StatusOK)
}

func (handler *HTTPHandler) listParts(response http.ResponseWriter, request *http.Request, store state.ObjectStore, objectKey, requestID string) {
	query := request.URL.Query()
	marker := 0
	if value := query.Get("part-number-marker"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 10_000 {
			writeS3Error(response, http.StatusBadRequest, "InvalidArgument", "part-number-marker is invalid", requestID)
			return
		}
		marker = parsed
	}
	maximum := 1000
	if value := query.Get("max-parts"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 1000 {
			writeS3Error(response, http.StatusBadRequest, "InvalidArgument", "max-parts must be 1..1000", requestID)
			return
		}
		maximum = parsed
	}
	upload, parts, more, err := handler.application.Parts(request.Context(), store.ID, query.Get("uploadId"), objectKey, marker, maximum)
	if err != nil {
		writeMultipartError(response, err, requestID)
		return
	}
	nextMarker := 0
	if more && len(parts) != 0 {
		nextMarker = parts[len(parts)-1].PartNumber
	}
	result := listPartsResult{
		Bucket: store.BucketName, Key: objectKey, UploadID: upload.ID,
		PartNumberMarker: marker, NextPartNumberMarker: nextMarker,
		MaxParts: maximum, IsTruncated: more, Parts: make([]listPartEntry, 0, len(parts)),
	}
	for _, part := range parts {
		result.Parts = append(result.Parts, listPartEntry{
			PartNumber: part.PartNumber, ETag: `"` + part.ChecksumSHA256 + `"`,
			Size: part.PlaintextSize, LastModified: time.UnixMilli(upload.CreatedAtMillis).UTC().Format(time.RFC3339),
		})
	}
	writeS3XML(response, http.StatusOK, result)
}

func (handler *HTTPHandler) completeMultipart(response http.ResponseWriter, request *http.Request, store state.ObjectStore, objectKey, requestID string) {
	request.Body = http.MaxBytesReader(response, request.Body, 1<<20)
	decoder := xml.NewDecoder(request.Body)
	var input completeMultipartUploadRequest
	if err := decoder.Decode(&input); err != nil {
		writeS3Error(response, http.StatusBadRequest, "MalformedXML", "Multipart completion XML is invalid", requestID)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeS3Error(response, http.StatusBadRequest, "MalformedXML", "Multipart completion XML has trailing data", requestID)
		return
	}
	parts := make([]CompletedPart, len(input.Parts))
	for index, part := range input.Parts {
		parts[index] = CompletedPart{PartNumber: part.PartNumber, ETag: part.ETag}
	}
	metadata, err := handler.application.CompleteMultipart(request.Context(), store.ID, request.URL.Query().Get("uploadId"), objectKey, parts)
	if err != nil {
		writeMultipartError(response, err, requestID)
		return
	}
	writeS3XML(response, http.StatusOK, completeMultipartUploadResult{
		Bucket: store.BucketName, Key: objectKey, ETag: metadata.ETag,
	})
}

func (handler *HTTPHandler) abortMultipart(response http.ResponseWriter, request *http.Request, store state.ObjectStore, objectKey, requestID string) {
	if err := handler.application.AbortMultipart(request.Context(), store.ID, request.URL.Query().Get("uploadId"), objectKey); err != nil {
		writeMultipartError(response, err, requestID)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func writeMultipartError(response http.ResponseWriter, err error, requestID string) {
	switch {
	case errors.Is(err, state.ErrMultipartUploadNotFound):
		writeS3Error(response, http.StatusNotFound, "NoSuchUpload", "The multipart upload does not exist", requestID)
	case errors.Is(err, ErrBadDigest):
		writeS3Error(response, http.StatusBadRequest, "BadDigest", "Part checksum does not match", requestID)
	case errors.Is(err, ErrMetadataMaintenance):
		writeS3Error(response, http.StatusConflict, "OperationAborted", "Object store restore is in progress", requestID)
	case errors.Is(err, ErrInvalidInput):
		writeS3Error(response, http.StatusBadRequest, "InvalidPart", err.Error(), requestID)
	default:
		writeS3Error(response, http.StatusInternalServerError, "InternalError", "Unable to process multipart upload", requestID)
	}
}

type createMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	XMLNS    string   `xml:"xmlns,attr,omitempty"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

type completeMultipartUploadRequest struct {
	XMLName xml.Name                    `xml:"CompleteMultipartUpload"`
	Parts   []completeMultipartPartItem `xml:"Part"`
}

type completeMultipartPartItem struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type completeMultipartUploadResult struct {
	XMLName xml.Name `xml:"CompleteMultipartUploadResult"`
	Bucket  string   `xml:"Bucket"`
	Key     string   `xml:"Key"`
	ETag    string   `xml:"ETag"`
}

type listPartsResult struct {
	XMLName              xml.Name        `xml:"ListPartsResult"`
	Bucket               string          `xml:"Bucket"`
	Key                  string          `xml:"Key"`
	UploadID             string          `xml:"UploadId"`
	PartNumberMarker     int             `xml:"PartNumberMarker"`
	NextPartNumberMarker int             `xml:"NextPartNumberMarker,omitempty"`
	MaxParts             int             `xml:"MaxParts"`
	IsTruncated          bool            `xml:"IsTruncated"`
	Parts                []listPartEntry `xml:"Part"`
}

type listPartEntry struct {
	PartNumber   int    `xml:"PartNumber"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
}
