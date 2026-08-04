package objectstore

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Storage interface {
	ReconcileBuckets(context.Context, []string) error
	EnsureBucket(context.Context, string) error
	DeleteBucket(context.Context, string) error
	ClearBucket(context.Context, string) error
	Put(context.Context, PutInput) (ObjectMetadata, error)
	Object(context.Context, string, string) (ObjectMetadata, error)
	ReadRange(context.Context, string, string, int64, int64, io.Writer) error
	ListEntries(context.Context, string, string, string, string, int) ([]ObjectListEntry, bool, error)
	Stats(context.Context, string) (ObjectStoreStats, error)
	LargestObjects(context.Context, string) (LargestObjectsSearch, error)
	StartLargestObjects(context.Context, string) (LargestObjectsSearch, error)
	CancelLargestObjects(context.Context, string) (LargestObjectsSearch, error)
	Delete(context.Context, string, string) error
}

type ObjectStoreStats struct {
	Ready               bool                        `json:"ready"`
	ObjectCount         uint64                      `json:"objectCount"`
	TotalBytes          uint64                      `json:"totalBytes"`
	ObservedAtMillis    int64                       `json:"observedAt,omitempty"`
	ObjectSizeHistogram []ObjectSizeHistogramBucket `json:"objectSizeHistogram"`
}

type ObjectSizeHistogramBucket struct {
	Label string `json:"label"`
	Count uint64 `json:"count"`
}

type LargestObjectsSearchStatus string

const (
	LargestObjectsIdle       LargestObjectsSearchStatus = "idle"
	LargestObjectsRunning    LargestObjectsSearchStatus = "running"
	LargestObjectsCancelling LargestObjectsSearchStatus = "cancelling"
	LargestObjectsComplete   LargestObjectsSearchStatus = "complete"
	LargestObjectsCancelled  LargestObjectsSearchStatus = "cancelled"
	LargestObjectsFailed     LargestObjectsSearchStatus = "failed"
)

type LargestObjectsSearch struct {
	Status         LargestObjectsSearchStatus `json:"status"`
	ScannedObjects uint64                     `json:"scannedObjects"`
	Objects        []LargestObject            `json:"objects"`
	Error          string                     `json:"error,omitempty"`
}

type LargestObject struct {
	Key  string `json:"key"`
	Size uint64 `json:"size"`
}

type SidecarClient struct {
	http *http.Client
}

type DataPlaneStore struct {
	StoreID     string   `json:"storeId"`
	BucketName  string   `json:"bucketName"`
	AccessKey   string   `json:"accessKey"`
	Secret      string   `json:"secret"`
	Permission  string   `json:"permission"`
	CORSOrigins []string `json:"corsOrigins"`
}

func NewSidecarClient(socketPath string) (*SidecarClient, error) {
	if socketPath == "" {
		return nil, errors.New("object store sidecar socket path is required")
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
		ReadBufferSize:      32 << 10,
		WriteBufferSize:     32 << 10,
	}
	return &SidecarClient{http: &http.Client{Transport: transport}}, nil
}

func (client *SidecarClient) Health(ctx context.Context) error {
	response, err := client.request(ctx, http.MethodGet, "/health", "", nil, nil)
	if err == nil {
		response.Body.Close()
	}
	return err
}

func (client *SidecarClient) ConfigureDataPlaneProject(ctx context.Context, projectID, listenAddress string, stores []DataPlaneStore) error {
	if projectID == "" || listenAddress == "" {
		return errors.New("object store data-plane configuration is incomplete")
	}
	if stores == nil {
		stores = []DataPlaneStore{}
	}
	for index := range stores {
		if stores[index].CORSOrigins == nil {
			stores[index].CORSOrigins = []string{}
		}
	}
	response, err := client.jsonRequest(ctx, http.MethodPut, "/v1/data-plane/project", "", struct {
		ProjectID     string           `json:"projectId"`
		ListenAddress string           `json:"listenAddress"`
		Stores        []DataPlaneStore `json:"stores"`
	}{projectID, listenAddress, stores})
	if err == nil {
		response.Body.Close()
	}
	return err
}

func (client *SidecarClient) RemoveDataPlaneProject(ctx context.Context, projectID string) error {
	if projectID == "" {
		return errors.New("object store data-plane project ID is required")
	}
	headers := make(http.Header)
	headers.Set("X-Platformd-Project", projectID)
	return client.noContent(ctx, http.MethodDelete, "/v1/data-plane/project", "", headers, nil)
}

func (client *SidecarClient) BeginDataPlaneBackup(ctx context.Context, storeID string) error {
	return client.noContent(ctx, http.MethodPost, "/v1/data-plane/store/backup/begin", storeID, nil, nil)
}

func (client *SidecarClient) EndDataPlaneBackup(ctx context.Context, storeID string) error {
	return client.noContent(ctx, http.MethodPost, "/v1/data-plane/store/backup/end", storeID, nil, nil)
}

func (client *SidecarClient) BeginDataPlaneRestore(ctx context.Context, storeID string) error {
	return client.noContent(ctx, http.MethodPost, "/v1/data-plane/store/restore/begin", storeID, nil, nil)
}

func (client *SidecarClient) EndDataPlaneRestore(ctx context.Context, storeID string) error {
	return client.noContent(ctx, http.MethodPost, "/v1/data-plane/store/restore/end", storeID, nil, nil)
}

func (client *SidecarClient) BeginDataPlaneQuiesce(ctx context.Context) error {
	return client.noContent(ctx, http.MethodPost, "/v1/data-plane/quiesce/begin", "", nil, nil)
}

func (client *SidecarClient) EndDataPlaneQuiesce(ctx context.Context) error {
	return client.noContent(ctx, http.MethodPost, "/v1/data-plane/quiesce/end", "", nil, nil)
}

func (client *SidecarClient) EnsureBucket(ctx context.Context, storeID string) error {
	return client.noContent(ctx, http.MethodPost, "/v1/bucket/ensure", storeID, nil, nil)
}

func (client *SidecarClient) ReconcileBuckets(ctx context.Context, storeIDs []string) error {
	if storeIDs == nil {
		storeIDs = []string{}
	}
	response, err := client.jsonRequest(ctx, http.MethodPost, "/v1/buckets/reconcile", "", struct {
		StoreIDs []string `json:"storeIds"`
	}{storeIDs})
	if err == nil {
		response.Body.Close()
	}
	return err
}

func (client *SidecarClient) DeleteBucket(ctx context.Context, storeID string) error {
	return client.noContent(ctx, http.MethodDelete, "/v1/bucket", storeID, nil, nil)
}

func (client *SidecarClient) ClearBucket(ctx context.Context, storeID string) error {
	return client.noContent(ctx, http.MethodPost, "/v1/bucket/clear", storeID, nil, nil)
}

func (client *SidecarClient) Put(ctx context.Context, input PutInput) (ObjectMetadata, error) {
	headers := objectHeaders(input.StoreID, input.ObjectKey)
	headers.Set("X-Platformd-Content-Type", encodeInternal(input.ContentType))
	headers.Set("X-Platformd-Size", "-1")
	if input.BodySizeKnown {
		headers.Set("X-Platformd-Size", strconv.FormatInt(input.BodySize, 10))
	}
	if input.ExpectedSHA256 != "" {
		headers.Set("X-Platformd-Sha256", input.ExpectedSHA256)
	}
	if input.PreserveETag != "" {
		headers.Set("X-Platformd-Preserve-Etag", encodeInternal(strings.Trim(input.PreserveETag, `"`)))
	}
	if input.ModTimeMillis != 0 {
		headers.Set("X-Platformd-Mod-Time", strconv.FormatInt(input.ModTimeMillis, 10))
	}
	applyPrecondition(headers, input.Precondition)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://platformd/v1/object", input.Body)
	if err != nil {
		return ObjectMetadata{}, err
	}
	request.Header = headers
	if input.BodySizeKnown {
		request.ContentLength = input.BodySize
	}
	response, err := client.http.Do(request)
	if err != nil {
		return ObjectMetadata{}, err
	}
	defer response.Body.Close()
	if err := decodeSidecarError(response); err != nil {
		return ObjectMetadata{}, err
	}
	return decodeObjectMetadata(response.Header, input.StoreID)
}

func (client *SidecarClient) Object(ctx context.Context, storeID, objectKey string) (ObjectMetadata, error) {
	response, err := client.request(ctx, http.MethodHead, "/v1/object", storeID, objectHeaders(storeID, objectKey), nil)
	if err != nil {
		return ObjectMetadata{}, err
	}
	defer response.Body.Close()
	return decodeObjectMetadata(response.Header, storeID)
}

func (client *SidecarClient) ReadRange(ctx context.Context, storeID, objectKey string, offset, length int64, output io.Writer) error {
	if length == 0 {
		return nil
	}
	headers := objectHeaders(storeID, objectKey)
	headers.Set("X-Platformd-Offset", strconv.FormatInt(offset, 10))
	headers.Set("X-Platformd-Length", strconv.FormatInt(length, 10))
	response, err := client.request(ctx, http.MethodGet, "/v1/object", storeID, headers, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, err = io.Copy(output, response.Body)
	return err
}

func (client *SidecarClient) ListEntries(ctx context.Context, storeID, prefix, delimiter, after string, limit int) ([]ObjectListEntry, bool, error) {
	headers := make(http.Header)
	headers.Set("X-Platformd-Prefix", encodeInternal(prefix))
	headers.Set("X-Platformd-Delimiter", encodeInternal(delimiter))
	headers.Set("X-Platformd-After", encodeInternal(after))
	headers.Set("X-Platformd-Limit", strconv.Itoa(limit))
	response, err := client.request(ctx, http.MethodGet, "/v1/objects", storeID, headers, nil)
	if err != nil {
		return nil, false, err
	}
	defer response.Body.Close()
	var result struct {
		Objects  []sidecarObject `json:"objects"`
		Prefixes []string        `json:"prefixes"`
		More     bool            `json:"more"`
	}
	if err := decodeJSON(response.Body, &result); err != nil {
		return nil, false, err
	}
	entries := make([]ObjectListEntry, 0, len(result.Objects)+len(result.Prefixes))
	for _, item := range result.Objects {
		metadata := item.metadata(storeID)
		entries = append(entries, ObjectListEntry{Object: &metadata})
	}
	for _, prefix := range result.Prefixes {
		entries = append(entries, ObjectListEntry{CommonPrefix: prefix})
	}
	sortObjectEntries(entries)
	return entries, result.More, nil
}

func (client *SidecarClient) Stats(ctx context.Context, storeID string) (ObjectStoreStats, error) {
	response, err := client.request(ctx, http.MethodGet, "/v1/bucket/stats", storeID, nil, nil)
	if err != nil {
		return ObjectStoreStats{}, err
	}
	defer response.Body.Close()
	var result ObjectStoreStats
	if err := decodeJSON(response.Body, &result); err != nil {
		return ObjectStoreStats{}, err
	}
	return result, nil
}

func (client *SidecarClient) LargestObjects(ctx context.Context, storeID string) (LargestObjectsSearch, error) {
	return client.largestObjectsRequest(ctx, http.MethodGet, storeID)
}

func (client *SidecarClient) StartLargestObjects(ctx context.Context, storeID string) (LargestObjectsSearch, error) {
	return client.largestObjectsRequest(ctx, http.MethodPost, storeID)
}

func (client *SidecarClient) CancelLargestObjects(ctx context.Context, storeID string) (LargestObjectsSearch, error) {
	return client.largestObjectsRequest(ctx, http.MethodDelete, storeID)
}

func (client *SidecarClient) largestObjectsRequest(ctx context.Context, method, storeID string) (LargestObjectsSearch, error) {
	response, err := client.request(ctx, method, "/v1/bucket/largest-objects", storeID, nil, nil)
	if err != nil {
		return LargestObjectsSearch{}, err
	}
	defer response.Body.Close()
	var result LargestObjectsSearch
	if err := decodeJSON(response.Body, &result); err != nil {
		return LargestObjectsSearch{}, err
	}
	return result, nil
}

func (client *SidecarClient) Delete(ctx context.Context, storeID, objectKey string) error {
	return client.noContent(ctx, http.MethodDelete, "/v1/object", storeID, objectHeaders(storeID, objectKey), nil)
}

func (client *SidecarClient) noContent(ctx context.Context, method, path, storeID string, headers http.Header, body io.Reader) error {
	response, err := client.request(ctx, method, path, storeID, headers, body)
	if err == nil {
		response.Body.Close()
	}
	return err
}

func (client *SidecarClient) jsonRequest(ctx context.Context, method, path, storeID string, value any) (*http.Response, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	return client.request(ctx, method, path, storeID, headers, bytes.NewReader(body))
}

func (client *SidecarClient) request(ctx context.Context, method, path, storeID string, headers http.Header, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, "http://platformd"+path, body)
	if err != nil {
		return nil, err
	}
	if headers != nil {
		request.Header = headers.Clone()
	}
	if storeID != "" {
		request.Header.Set("X-Platformd-Store", storeID)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, err
	}
	if err := decodeSidecarError(response); err != nil {
		response.Body.Close()
		return nil, err
	}
	return response, nil
}

type sidecarObject struct {
	Key             string `json:"key"`
	ContentType     string `json:"contentType"`
	ETag            string `json:"etag"`
	Size            int64  `json:"size"`
	UpdatedAtMillis int64  `json:"updatedAtMillis"`
}

func (object sidecarObject) metadata(storeID string) ObjectMetadata {
	return ObjectMetadata{
		ObjectStoreID: storeID, ObjectKey: object.Key, ContentType: object.ContentType,
		ETag: quoteETag(object.ETag), Size: object.Size,
		CreatedAtMillis: object.UpdatedAtMillis, UpdatedAtMillis: object.UpdatedAtMillis,
	}
}

func decodeObjectMetadata(headers http.Header, storeID string) (ObjectMetadata, error) {
	key, err := decodeInternal(headers.Get("X-Platformd-Key"))
	if err != nil {
		return ObjectMetadata{}, err
	}
	contentType, err := decodeInternal(headers.Get("X-Platformd-Content-Type"))
	if err != nil {
		return ObjectMetadata{}, err
	}
	etag, err := decodeInternal(headers.Get("X-Platformd-Etag"))
	if err != nil {
		return ObjectMetadata{}, err
	}
	size, err := strconv.ParseInt(headers.Get("X-Platformd-Size"), 10, 64)
	if err != nil {
		return ObjectMetadata{}, errors.New("object store sidecar returned an invalid size")
	}
	updated, err := strconv.ParseInt(headers.Get("X-Platformd-Updated-At"), 10, 64)
	if err != nil {
		return ObjectMetadata{}, errors.New("object store sidecar returned an invalid timestamp")
	}
	return ObjectMetadata{
		ObjectStoreID: storeID, ObjectKey: key, ContentType: contentType,
		ETag: quoteETag(etag), Size: size, CreatedAtMillis: updated, UpdatedAtMillis: updated,
	}, nil
}

func decodeSidecarError(response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	limited := io.LimitReader(response.Body, 1<<20)
	var value struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	decodeErr := json.NewDecoder(limited).Decode(&value)
	if value.Code == "" {
		value.Code = response.Header.Get("X-Platformd-Error-Code")
	}
	if value.Code == "" && decodeErr != nil {
		return fmt.Errorf("object store sidecar returned HTTP %d", response.StatusCode)
	}
	if value.Message == "" {
		value.Message = http.StatusText(response.StatusCode)
	}
	base := errorForSidecarCode(value.Code)
	if base == nil {
		base = errors.New("object store sidecar operation failed")
	}
	return fmt.Errorf("%w: %s", base, value.Message)
}

func errorForSidecarCode(code string) error {
	switch code {
	case "object_not_found":
		return ErrObjectNotFound
	case "precondition_failed":
		return ErrPreconditionFailed
	case "bad_digest":
		return ErrBadDigest
	case "invalid_input", "invalid_part":
		return ErrInvalidInput
	default:
		return nil
	}
}

func objectHeaders(storeID, objectKey string) http.Header {
	headers := make(http.Header)
	headers.Set("X-Platformd-Store", storeID)
	headers.Set("X-Platformd-Key", encodeInternal(objectKey))
	return headers
}

func applyPrecondition(headers http.Header, precondition ObjectPrecondition) {
	if precondition.IfMatch != "" {
		headers.Set("X-Platformd-If-Match", precondition.IfMatch)
	}
	if precondition.IfNoneMatch {
		headers.Set("X-Platformd-If-None-Match", "true")
	}
}

func encodeInternal(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeInternal(value string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", errors.New("object store sidecar returned invalid base64 metadata")
	}
	return string(decoded), nil
}

func quoteETag(value string) string {
	value = strings.Trim(value, `"`)
	if value == "" {
		return ""
	}
	return `"` + value + `"`
}

func decodeJSON(reader io.Reader, output any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("object store sidecar JSON has trailing data")
	}
	return nil
}

func sortObjectEntries(entries []ObjectListEntry) {
	slices.SortFunc(entries, func(left, right ObjectListEntry) int {
		return strings.Compare(objectEntryKey(left), objectEntryKey(right))
	})
}

func objectEntryKey(entry ObjectListEntry) string {
	if entry.Object != nil {
		return entry.Object.ObjectKey
	}
	return entry.CommonPrefix
}
