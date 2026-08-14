package telemetry

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/containerlogs"
)

const (
	logDownloadFooterReserve = 256
	logPageResponseBytes     = 140 << 20
)

var telemetryLogID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var telemetryStoredLogID = regexp.MustCompile(`^[[:xdigit:]]{8}(?:-[[:xdigit:]]{4}){3}-[[:xdigit:]]{12}$`)
var telemetryTraceID = regexp.MustCompile(`^[[:xdigit:]]{32}$`)
var telemetrySpanID = regexp.MustCompile(`^[[:xdigit:]]{16}$`)
var telemetryLogFieldPath = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)

type LogReader struct {
	endpoint string
	client   *http.Client
}

type telemetryLogRecord struct {
	ID             string  `json:"id"`
	TimeUnixNano   uint64  `json:"timeUnixNano"`
	Stream         string  `json:"stream"`
	Text           string  `json:"text"`
	Partial        bool    `json:"partial"`
	DeploymentID   string  `json:"deploymentId"`
	AttemptID      string  `json:"attemptId"`
	TraceID        string  `json:"traceId"`
	SpanID         string  `json:"spanId"`
	SeverityText   string  `json:"severityText"`
	SeverityNumber int32   `json:"severityNumber"`
	BodyJSON       *string `json:"bodyJson"`
	Phase          string  `json:"phase"`
}

type telemetryLogPage struct {
	Records          []telemetryLogRecord `json:"records"`
	Truncated        bool                 `json:"truncated"`
	NextTimeUnixNano *uint64              `json:"nextTimeUnixNano"`
	NextID           *string              `json:"nextId"`
}

type telemetryLogCursor struct {
	TimeUnixNano uint64 `json:"t"`
	ID           string `json:"i"`
}

type telemetryLogRequest struct {
	serviceID         string
	deploymentID      string
	contains          string
	fieldFilters      []containerlogs.FieldFilter
	severityText      string
	traceID           string
	spanID            string
	from              *time.Time
	to                *time.Time
	afterTimeUnixNano *uint64
	afterID           string
	limit             int
	ascending         bool
}

func NewLogReader(endpoint string) (*LogReader, error) {
	if !validHTTPEndpoint(endpoint) {
		return nil, errors.New("telemetry log reader endpoint is invalid")
	}
	return &LogReader{
		endpoint: endpoint,
		client:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (reader *LogReader) Read(ctx context.Context, query containerlogs.Query) (containerlogs.Window, error) {
	if err := validateTelemetryLogQuery(query); err != nil {
		return containerlogs.Window{}, err
	}
	cursor, err := decodeTelemetryLogCursor(query.Cursor)
	if err != nil {
		return containerlogs.Window{}, err
	}
	limit := query.Limit
	if limit == 0 {
		limit = containerlogs.DefaultLimit
	}
	page, err := reader.page(ctx, telemetryLogRequest{
		serviceID: query.ServiceID, deploymentID: query.DeploymentID,
		contains: query.Contains, fieldFilters: query.FieldFilters, severityText: query.SeverityText,
		traceID: strings.ToLower(query.TraceID), spanID: strings.ToLower(query.SpanID),
		afterTimeUnixNano: cursorTime(cursor), afterID: cursorID(cursor),
		from: optionalTime(query.From), to: optionalTime(query.To),
		limit: limit, ascending: query.Ascending,
	})
	if err != nil {
		return containerlogs.Window{}, err
	}
	records, err := publicLogRecords(page.Records)
	if err != nil {
		return containerlogs.Window{}, err
	}
	window := containerlogs.Window{Records: records, Truncated: page.Truncated}
	if page.Truncated {
		if page.NextTimeUnixNano == nil || *page.NextTimeUnixNano == 0 || page.NextID == nil || !telemetryStoredLogID.MatchString(*page.NextID) {
			return containerlogs.Window{}, errors.New("telemetry log page omitted its continuation cursor")
		}
		window.NextCursor = encodeTelemetryLogCursor(telemetryLogCursor{
			TimeUnixNano: *page.NextTimeUnixNano,
			ID:           *page.NextID,
		})
	}
	return window, nil
}

func decodeTelemetryLogCursor(value string) (*telemetryLogCursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > 512 {
		return nil, fmt.Errorf("%w: invalid telemetry log cursor", containerlogs.ErrInvalidQuery)
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid telemetry log cursor", containerlogs.ErrInvalidQuery)
	}
	var cursor telemetryLogCursor
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.TimeUnixNano == 0 || !telemetryStoredLogID.MatchString(cursor.ID) {
		return nil, fmt.Errorf("%w: invalid telemetry log cursor", containerlogs.ErrInvalidQuery)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("%w: invalid telemetry log cursor", containerlogs.ErrInvalidQuery)
	}
	return &cursor, nil
}

func encodeTelemetryLogCursor(cursor telemetryLogCursor) string {
	// Cursor IDs are validated as URL-safe telemetry IDs, so this fixed JSON
	// shape cannot require additional string escaping.
	payload := []byte(fmt.Sprintf(`{"t":%d,"i":"%s"}`, cursor.TimeUnixNano, cursor.ID))
	return base64.RawURLEncoding.EncodeToString(payload)
}

func cursorTime(cursor *telemetryLogCursor) *uint64 {
	if cursor == nil {
		return nil
	}
	return &cursor.TimeUnixNano
}

func cursorID(cursor *telemetryLogCursor) string {
	if cursor == nil {
		return ""
	}
	return cursor.ID
}

func (reader *LogReader) Download(
	ctx context.Context,
	query containerlogs.DownloadQuery,
	destination io.Writer,
) (containerlogs.DownloadResult, error) {
	if destination == nil {
		return containerlogs.DownloadResult{}, errors.New("container log download destination is required")
	}
	if !telemetryLogID.MatchString(query.ServiceID) ||
		(query.DeploymentID != "" && !telemetryLogID.MatchString(query.DeploymentID)) ||
		query.From.IsZero() || query.To.IsZero() || !query.To.After(query.From) ||
		query.To.Sub(query.From) > containerlogs.MaximumDownloadRange {
		return containerlogs.DownloadResult{}, fmt.Errorf("%w: invalid telemetry log download query", containerlogs.ErrInvalidQuery)
	}
	result := containerlogs.DownloadResult{}
	dataLimit := int64(containerlogs.MaximumDownloadBytes - logDownloadFooterReserve)
	if err := writeTelemetryLogLine(destination, &result.Bytes, dataLimit, struct {
		Type         string    `json:"type"`
		From         time.Time `json:"from"`
		To           time.Time `json:"to"`
		ServiceID    string    `json:"serviceId"`
		DeploymentID string    `json:"deploymentId,omitempty"`
	}{"platformd.log_export", query.From, query.To, query.ServiceID, query.DeploymentID}); err != nil {
		return result, err
	}
	var afterTime *uint64
	var afterID string
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		page, err := reader.page(ctx, telemetryLogRequest{
			serviceID: query.ServiceID, deploymentID: query.DeploymentID,
			from: &query.From, to: &query.To, afterTimeUnixNano: afterTime, afterID: afterID,
			limit: containerlogs.MaximumLimit, ascending: true,
		})
		if err != nil {
			return result, err
		}
		for _, stored := range page.Records {
			record, err := publicLogRecord(stored)
			if err != nil {
				return result, err
			}
			line := struct {
				Type string `json:"type"`
				containerlogs.Record
			}{Type: "record", Record: record}
			if err := writeTelemetryLogLine(destination, &result.Bytes, dataLimit, line); errors.Is(err, errTelemetryLogDownloadLimit) {
				result.Truncated = true
				break
			} else if err != nil {
				return result, err
			}
			result.Records++
		}
		if result.Truncated || !page.Truncated {
			break
		}
		if page.NextTimeUnixNano == nil || page.NextID == nil {
			return result, errors.New("telemetry log page omitted its continuation cursor")
		}
		afterTime = page.NextTimeUnixNano
		afterID = *page.NextID
	}
	if err := writeTelemetryLogLine(destination, &result.Bytes, containerlogs.MaximumDownloadBytes, struct {
		Type      string `json:"type"`
		Records   int    `json:"records"`
		Truncated bool   `json:"truncated"`
	}{"platformd.log_export_complete", result.Records, result.Truncated}); err != nil {
		return result, err
	}
	return result, nil
}

func (reader *LogReader) page(ctx context.Context, input telemetryLogRequest) (telemetryLogPage, error) {
	endpoint, err := url.Parse(reader.endpoint + "/internal/logs")
	if err != nil {
		return telemetryLogPage{}, err
	}
	query := endpoint.Query()
	query.Set("serviceId", input.serviceID)
	query.Set("limit", strconv.Itoa(input.limit))
	if input.deploymentID != "" {
		query.Set("deploymentId", input.deploymentID)
	}
	if input.contains != "" {
		query.Set("contains", input.contains)
	}
	if len(input.fieldFilters) > 0 {
		encoded, err := json.Marshal(input.fieldFilters)
		if err != nil {
			return telemetryLogPage{}, fmt.Errorf("encode telemetry log field filters: %w", err)
		}
		query.Set("fieldFilters", string(encoded))
	}
	if input.severityText != "" {
		query.Set("severityText", input.severityText)
	}
	if input.traceID != "" {
		query.Set("traceId", input.traceID)
	}
	if input.spanID != "" {
		query.Set("spanId", input.spanID)
	}
	if input.from != nil {
		query.Set("from", strconv.FormatInt(input.from.UnixMilli(), 10))
	}
	if input.to != nil {
		query.Set("to", strconv.FormatInt(input.to.UnixMilli(), 10))
	}
	if input.afterTimeUnixNano != nil {
		query.Set("afterTimeUnixNano", strconv.FormatUint(*input.afterTimeUnixNano, 10))
		query.Set("afterId", input.afterID)
	}
	if input.ascending {
		query.Set("order", "asc")
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return telemetryLogPage{}, err
	}
	response, err := reader.client.Do(request)
	if err != nil {
		return telemetryLogPage{}, fmt.Errorf("query telemetry logs: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return telemetryLogPage{}, fmt.Errorf("query telemetry logs returned HTTP %d", response.StatusCode)
	}
	var page telemetryLogPage
	decoder := json.NewDecoder(io.LimitReader(response.Body, logPageResponseBytes+1))
	if err := decoder.Decode(&page); err != nil {
		return telemetryLogPage{}, fmt.Errorf("decode telemetry log page: %w", err)
	}
	return page, nil
}

func validateTelemetryLogQuery(query containerlogs.Query) error {
	if !telemetryLogID.MatchString(query.ServiceID) ||
		(query.DeploymentID != "" && !telemetryLogID.MatchString(query.DeploymentID)) ||
		len(query.Contains) > containerlogs.MaximumContainsBytes || bytes.IndexByte([]byte(query.Contains), 0) >= 0 ||
		len(query.SeverityText) > 64 || bytes.IndexByte([]byte(query.SeverityText), 0) >= 0 ||
		!validTelemetryLogFieldFilters(query.FieldFilters) ||
		(query.TraceID != "" && !telemetryTraceID.MatchString(query.TraceID)) ||
		(query.SpanID != "" && !telemetrySpanID.MatchString(query.SpanID)) ||
		(!query.From.IsZero() && !query.To.IsZero() && query.To.Before(query.From)) ||
		query.Limit < 0 || query.Limit > containerlogs.MaximumLimit {
		return fmt.Errorf("%w: invalid telemetry log query", containerlogs.ErrInvalidQuery)
	}
	return nil
}

func validTelemetryLogFieldFilters(filters []containerlogs.FieldFilter) bool {
	if len(filters) > containerlogs.MaximumFieldFilters {
		return false
	}
	for _, filter := range filters {
		if len(filter.Path) > 256 || !telemetryLogFieldPath.MatchString(filter.Path) ||
			len(filter.Value) > 512 || strings.IndexByte(filter.Value, 0) >= 0 {
			return false
		}
		switch filter.Operator {
		case "equals":
		case "contains":
			if filter.Value == "" {
				return false
			}
		case "exists":
			if filter.Value != "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func publicLogRecords(stored []telemetryLogRecord) ([]containerlogs.Record, error) {
	records := make([]containerlogs.Record, 0, len(stored))
	for _, item := range stored {
		record, err := publicLogRecord(item)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func publicLogRecord(stored telemetryLogRecord) (containerlogs.Record, error) {
	if stored.TimeUnixNano > math.MaxInt64 {
		return containerlogs.Record{}, errors.New("telemetry log timestamp exceeds Go time")
	}
	stream := stored.Stream
	if stream == "" {
		stream = "otel"
	}
	record := containerlogs.Record{
		Timestamp: time.Unix(0, int64(stored.TimeUnixNano)).UTC(), Stream: stream, Text: stored.Text,
		DeploymentID: stored.DeploymentID, AttemptID: stored.AttemptID, Partial: stored.Partial,
		TraceID: stored.TraceID, SpanID: stored.SpanID, SeverityText: stored.SeverityText,
		SeverityNumber: stored.SeverityNumber, Phase: stored.Phase,
	}
	if stored.BodyJSON != nil {
		if err := json.Unmarshal([]byte(*stored.BodyJSON), &record.Fields); err != nil {
			return containerlogs.Record{}, fmt.Errorf("decode structured telemetry log body: %w", err)
		}
	}
	return record, nil
}

var errTelemetryLogDownloadLimit = errors.New("telemetry log download byte limit reached")

func writeTelemetryLogLine(destination io.Writer, written *int64, limit int64, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode telemetry log export: %w", err)
	}
	payload = append(payload, '\n')
	if *written+int64(len(payload)) > limit {
		return errTelemetryLogDownloadLimit
	}
	count, err := destination.Write(payload)
	*written += int64(count)
	if err != nil {
		return fmt.Errorf("write telemetry log export: %w", err)
	}
	if count != len(payload) {
		return io.ErrShortWrite
	}
	return nil
}
