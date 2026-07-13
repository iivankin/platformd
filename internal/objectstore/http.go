package objectstore

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

type HostLookup func(context.Context, string) (state.ObjectStore, error)

type HTTPConfig struct {
	Application *Application
	LookupHost  HostLookup
	Now         func() time.Time
	Admission   *admission.Gate
}

type HTTPHandler struct {
	application *Application
	lookupHost  HostLookup
	verifier    *SigV4Verifier
	admission   *admission.Gate
}

func NewHTTPHandler(config HTTPConfig) (*HTTPHandler, error) {
	if config.Application == nil || config.LookupHost == nil || config.Admission == nil {
		return nil, errors.New("S3 HTTP handler dependencies are incomplete")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	verifier, err := NewSigV4Verifier(SigV4Config{
		Resolve: config.Application.Credential, Now: now,
	})
	if err != nil {
		return nil, err
	}
	return &HTTPHandler{application: config.Application, lookupHost: config.LookupHost, verifier: verifier, admission: config.Admission}, nil
}

func (handler *HTTPHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
	requestID, _ := id.New()
	response.Header().Set("X-Amz-Request-Id", requestID)

	host, err := canonicalRequestHost(request.Host)
	if err != nil {
		writeS3Error(response, http.StatusBadRequest, "InvalidRequest", "Host header is invalid", requestID)
		return
	}
	store, err := handler.lookupHost(request.Context(), host)
	if err != nil {
		writeS3Error(response, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist", requestID)
		return
	}
	bucket, objectKey, err := parseS3Path(request.URL.Path)
	if err != nil || bucket != store.BucketName {
		writeS3Error(response, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist", requestID)
		return
	}
	if request.Method == http.MethodOptions {
		handler.preflight(response, request, store, requestID)
		return
	}
	applyCORS(response, request, store)
	credential, err := handler.verifier.Verify(request.Context(), request)
	if err != nil {
		code := "SignatureDoesNotMatch"
		if errors.Is(err, ErrExpiredSignature) {
			code = "RequestTimeTooSkewed"
		}
		writeS3Error(response, http.StatusForbidden, code, "The request signature is not valid", requestID)
		return
	}
	if credential.Store.ID != store.ID {
		writeS3Error(response, http.StatusForbidden, "AccessDenied", "Credential does not belong to this bucket", requestID)
		return
	}
	write := request.Method == http.MethodPut || request.Method == http.MethodDelete || request.URL.Query().Has("uploads") || request.URL.Query().Has("uploadId")
	if write && credential.Permission != "read_write" {
		writeS3Error(response, http.StatusForbidden, "AccessDenied", "Credential is read-only", requestID)
		return
	}
	mutation := request.Method == http.MethodPut || request.Method == http.MethodDelete ||
		(request.Method == http.MethodPost && (request.URL.Query().Has("uploads") || request.URL.Query().Has("uploadId")))
	if mutation {
		lease, err := handler.admission.Begin("s3_mutation", store.ID)
		if err != nil {
			writeS3Error(response, http.StatusConflict, "OperationAborted", "Platform update is in progress", requestID)
			return
		}
		defer lease.Release()
	}
	handler.dispatch(response, request, store, objectKey, requestID)
}

func (handler *HTTPHandler) dispatch(response http.ResponseWriter, request *http.Request, store state.ObjectStore, objectKey, requestID string) {
	switch {
	case request.Method == http.MethodHead && objectKey == "":
		response.WriteHeader(http.StatusOK)
	case request.Method == http.MethodGet && objectKey == "" && request.URL.Query().Get("list-type") == "2":
		handler.listObjects(response, request, store, requestID)
	case request.Method == http.MethodPut && objectKey != "" && isPutObjectQuery(request.URL.Query()):
		handler.putObject(response, request, store, objectKey, requestID)
	case request.Method == http.MethodPost && objectKey != "" && request.URL.Query().Has("uploads"):
		handler.createMultipart(response, request, store, objectKey, requestID)
	case request.Method == http.MethodPut && objectKey != "" && request.URL.Query().Get("uploadId") != "":
		handler.uploadPart(response, request, store, objectKey, requestID)
	case request.Method == http.MethodGet && objectKey != "" && request.URL.Query().Get("uploadId") != "":
		handler.listParts(response, request, store, objectKey, requestID)
	case request.Method == http.MethodPost && objectKey != "" && request.URL.Query().Get("uploadId") != "":
		handler.completeMultipart(response, request, store, objectKey, requestID)
	case request.Method == http.MethodDelete && objectKey != "" && request.URL.Query().Get("uploadId") != "":
		handler.abortMultipart(response, request, store, objectKey, requestID)
	case request.Method == http.MethodGet && objectKey != "":
		handler.getObject(response, request, store, objectKey, false, requestID)
	case request.Method == http.MethodHead && objectKey != "":
		handler.getObject(response, request, store, objectKey, true, requestID)
	case request.Method == http.MethodDelete && objectKey != "" && request.URL.RawQuery == "":
		handler.deleteObject(response, request, store, objectKey, requestID)
	default:
		writeS3Error(response, http.StatusNotImplemented, "NotImplemented", "This S3 operation is not supported", requestID)
	}
}

func isPutObjectQuery(query url.Values) bool {
	if len(query) == 0 {
		return true
	}
	if query.Get("X-Amz-Algorithm") != signatureAlgorithm {
		return false
	}
	allowed := map[string]struct{}{
		"X-Amz-Algorithm": {}, "X-Amz-Credential": {}, "X-Amz-Date": {},
		"X-Amz-Expires": {}, "X-Amz-Signature": {}, "X-Amz-SignedHeaders": {},
		"x-id": {},
	}
	for name := range query {
		if _, ok := allowed[name]; !ok {
			return false
		}
	}
	return query.Get("x-id") == "" || query.Get("x-id") == "PutObject"
}

func (handler *HTTPHandler) putObject(response http.ResponseWriter, request *http.Request, store state.ObjectStore, objectKey, requestID string) {
	if request.ContentLength > MaximumObjectSize {
		writeS3Error(response, http.StatusRequestEntityTooLarge, "EntityTooLarge", "Object exceeds 100 GiB", requestID)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, MaximumObjectSize+1)
	expected := request.Header.Get("X-Amz-Content-Sha256")
	if expected == "UNSIGNED-PAYLOAD" {
		expected = ""
	}
	object, err := handler.application.Put(request.Context(), PutInput{
		StoreID: store.ID, ObjectKey: objectKey, ContentType: request.Header.Get("Content-Type"),
		ExpectedSHA256: expected, Body: request.Body,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrBadDigest):
			writeS3Error(response, http.StatusBadRequest, "BadDigest", "Payload checksum does not match", requestID)
		case errors.Is(err, ErrInvalidInput):
			writeS3Error(response, http.StatusBadRequest, "InvalidRequest", err.Error(), requestID)
		default:
			writeS3Error(response, http.StatusInternalServerError, "InternalError", "Unable to store object", requestID)
		}
		return
	}
	response.Header().Set("ETag", object.ETag)
	response.WriteHeader(http.StatusOK)
}

func (handler *HTTPHandler) getObject(response http.ResponseWriter, request *http.Request, store state.ObjectStore, objectKey string, head bool, requestID string) {
	object, err := handler.application.Object(request.Context(), store.ID, objectKey)
	if errors.Is(err, state.ErrObjectNotFound) {
		writeS3Error(response, http.StatusNotFound, "NoSuchKey", "The specified key does not exist", requestID)
		return
	}
	if err != nil {
		writeS3Error(response, http.StatusInternalServerError, "InternalError", "Unable to read object", requestID)
		return
	}
	offset, length, partial, err := parseRange(request.Header.Get("Range"), object.Metadata.Size)
	if err != nil {
		response.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", object.Metadata.Size))
		writeS3Error(response, http.StatusRequestedRangeNotSatisfiable, "InvalidRange", "Requested range is invalid", requestID)
		return
	}
	response.Header().Set("Accept-Ranges", "bytes")
	response.Header().Set("ETag", object.Metadata.ETag)
	response.Header().Set("Last-Modified", time.UnixMilli(object.Metadata.UpdatedAtMillis).UTC().Format(http.TimeFormat))
	if object.Metadata.ContentType != "" {
		response.Header().Set("Content-Type", object.Metadata.ContentType)
	} else {
		response.Header().Set("Content-Type", "application/octet-stream")
	}
	response.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
		response.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+length-1, object.Metadata.Size))
	}
	response.WriteHeader(status)
	if head || length == 0 {
		return
	}
	_ = handler.application.ReadRange(request.Context(), object, offset, length, response)
}

func (handler *HTTPHandler) deleteObject(response http.ResponseWriter, request *http.Request, store state.ObjectStore, objectKey, requestID string) {
	err := handler.application.Delete(request.Context(), store.ID, objectKey)
	if err != nil && !errors.Is(err, state.ErrObjectNotFound) {
		writeS3Error(response, http.StatusInternalServerError, "InternalError", "Unable to delete object", requestID)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (handler *HTTPHandler) listObjects(response http.ResponseWriter, request *http.Request, store state.ObjectStore, requestID string) {
	query := request.URL.Query()
	maxKeys := 1000
	if value := query.Get("max-keys"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 1000 {
			writeS3Error(response, http.StatusBadRequest, "InvalidArgument", "max-keys must be 0..1000", requestID)
			return
		}
		maxKeys = parsed
	}
	after := query.Get("start-after")
	if token := query.Get("continuation-token"); token != "" {
		if after != "" {
			writeS3Error(response, http.StatusBadRequest, "InvalidArgument", "continuation-token and start-after are mutually exclusive", requestID)
			return
		}
		var err error
		after, err = handler.application.DecodeContinuationToken(store.ID, token)
		if err != nil {
			writeS3Error(response, http.StatusBadRequest, "InvalidToken", "Continuation token is invalid", requestID)
			return
		}
	}
	objects, more, err := handler.application.List(request.Context(), store.ID, query.Get("prefix"), after, max(maxKeys, 1))
	if err != nil {
		writeS3Error(response, http.StatusInternalServerError, "InternalError", "Unable to list objects", requestID)
		return
	}
	if maxKeys == 0 {
		objects = nil
		more = false
	}
	nextToken := ""
	if more && len(objects) != 0 {
		nextToken, err = handler.application.EncodeContinuationToken(store.ID, objects[len(objects)-1].ObjectKey)
		if err != nil {
			writeS3Error(response, http.StatusInternalServerError, "InternalError", "Unable to paginate objects", requestID)
			return
		}
	}
	result := listBucketResult{
		XMLNS: "http://s3.amazonaws.com/doc/2006-03-01/", Name: store.BucketName,
		Prefix: query.Get("prefix"), KeyCount: len(objects), MaxKeys: maxKeys,
		IsTruncated: more, ContinuationToken: query.Get("continuation-token"),
		NextContinuationToken: nextToken, StartAfter: query.Get("start-after"),
		Contents: make([]listObjectEntry, 0, len(objects)),
	}
	for _, object := range objects {
		result.Contents = append(result.Contents, listObjectEntry{
			Key: object.ObjectKey, LastModified: time.UnixMilli(object.UpdatedAtMillis).UTC().Format(time.RFC3339),
			ETag: object.ETag, Size: object.Size, StorageClass: "STANDARD",
		})
	}
	response.Header().Set("Content-Type", "application/xml")
	response.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(response, xml.Header)
	_ = xml.NewEncoder(response).Encode(result)
}

type listBucketResult struct {
	XMLName               xml.Name          `xml:"ListBucketResult"`
	XMLNS                 string            `xml:"xmlns,attr"`
	Name                  string            `xml:"Name"`
	Prefix                string            `xml:"Prefix"`
	KeyCount              int               `xml:"KeyCount"`
	MaxKeys               int               `xml:"MaxKeys"`
	IsTruncated           bool              `xml:"IsTruncated"`
	ContinuationToken     string            `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string            `xml:"NextContinuationToken,omitempty"`
	StartAfter            string            `xml:"StartAfter,omitempty"`
	Contents              []listObjectEntry `xml:"Contents"`
}

type listObjectEntry struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

type s3Error struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	RequestID string   `xml:"RequestId"`
}

func writeS3Error(response http.ResponseWriter, status int, code, message, requestID string) {
	response.Header().Set("Content-Type", "application/xml")
	response.WriteHeader(status)
	_, _ = io.WriteString(response, xml.Header)
	_ = xml.NewEncoder(response).Encode(s3Error{Code: code, Message: message, RequestID: requestID})
}

func writeS3XML(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/xml")
	response.WriteHeader(status)
	_, _ = io.WriteString(response, xml.Header)
	_ = xml.NewEncoder(response).Encode(value)
}

func canonicalRequestHost(value string) (string, error) {
	host := value
	if parsedHost, port, err := net.SplitHostPort(value); err == nil {
		if port != "443" && port != "9000" {
			return "", errors.New("unexpected S3 host port")
		}
		host = parsedHost
	}
	host = strings.ToLower(host)
	if host == "" || strings.HasSuffix(host, ".") || strings.ContainsAny(host, "/\\\x00 ") {
		return "", errors.New("invalid S3 host")
	}
	return host, nil
}

func parseS3Path(path string) (string, string, error) {
	if !strings.HasPrefix(path, "/") {
		return "", "", errors.New("S3 path is not absolute")
	}
	value := strings.TrimPrefix(path, "/")
	bucket, objectKey, hasObject := strings.Cut(value, "/")
	decodedBucket, err := url.PathUnescape(bucket)
	if err != nil {
		return "", "", err
	}
	if !hasObject {
		return decodedBucket, "", nil
	}
	decodedKey, err := url.PathUnescape(objectKey)
	if err != nil {
		return "", "", err
	}
	return decodedBucket, decodedKey, nil
}

func parseRange(value string, size int64) (offset, length int64, partial bool, err error) {
	if value == "" {
		return 0, size, false, nil
	}
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") || size == 0 {
		return 0, 0, false, errors.New("unsupported range")
	}
	startValue, endValue, ok := strings.Cut(strings.TrimPrefix(value, "bytes="), "-")
	if !ok {
		return 0, 0, false, errors.New("invalid range")
	}
	if startValue == "" {
		suffix, parseErr := strconv.ParseInt(endValue, 10, 64)
		if parseErr != nil || suffix <= 0 {
			return 0, 0, false, errors.New("invalid suffix range")
		}
		suffix = min(suffix, size)
		return size - suffix, suffix, true, nil
	}
	start, parseErr := strconv.ParseInt(startValue, 10, 64)
	if parseErr != nil || start < 0 || start >= size {
		return 0, 0, false, errors.New("invalid range start")
	}
	end := size - 1
	if endValue != "" {
		end, parseErr = strconv.ParseInt(endValue, 10, 64)
		if parseErr != nil || end < start {
			return 0, 0, false, errors.New("invalid range end")
		}
		end = min(end, size-1)
	}
	return start, end - start + 1, true, nil
}
