package telemetry

import (
	"bytes"
	"context"
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
var telemetryTraceID = regexp.MustCompile(`^[[:xdigit:]]{32}$`)
var telemetrySpanID = regexp.MustCompile(`^[[:xdigit:]]{16}$`)

type LogReader struct {
	endpoint string
	client   *http.Client
}

type telemetryLogRecord struct {
	ID             string `json:"id"`
	TimeUnixNano   uint64 `json:"timeUnixNano"`
	Stream         string `json:"stream"`
	Text           string `json:"text"`
	Partial        bool   `json:"partial"`
	DeploymentID   string `json:"deploymentId"`
	AttemptID      string `json:"attemptId"`
	TraceID        string `json:"traceId"`
	SpanID         string `json:"spanId"`
	SeverityText   string `json:"severityText"`
	SeverityNumber int32  `json:"severityNumber"`
}

type telemetryLogPage struct {
	Records          []telemetryLogRecord `json:"records"`
	Truncated        bool                 `json:"truncated"`
	Revision         string               `json:"revision"`
	NextTimeUnixNano *uint64              `json:"nextTimeUnixNano"`
	NextID           *string              `json:"nextId"`
}

type telemetryLogRequest struct {
	serviceID         string
	deploymentID      string
	contains          string
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
	limit := query.Limit
	if limit == 0 {
		limit = containerlogs.DefaultLimit
	}
	page, err := reader.page(ctx, telemetryLogRequest{
		serviceID: query.ServiceID, deploymentID: query.DeploymentID,
		contains: query.Contains, severityText: query.SeverityText,
		traceID: strings.ToLower(query.TraceID), spanID: strings.ToLower(query.SpanID),
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
	return containerlogs.Window{Records: records, Truncated: page.Truncated}, nil
}

func (reader *LogReader) Revision(ctx context.Context, query containerlogs.Query) (string, error) {
	query.Limit = 1
	if err := validateTelemetryLogQuery(query); err != nil {
		return "", err
	}
	page, err := reader.page(ctx, telemetryLogRequest{
		serviceID: query.ServiceID, deploymentID: query.DeploymentID,
		contains: query.Contains, severityText: query.SeverityText,
		traceID: query.TraceID, spanID: query.SpanID,
		from: optionalTime(query.From), to: optionalTime(query.To), limit: 1,
	})
	if err != nil {
		return "", err
	}
	return page.Revision, nil
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
		(query.TraceID != "" && !telemetryTraceID.MatchString(query.TraceID)) ||
		(query.SpanID != "" && !telemetrySpanID.MatchString(query.SpanID)) ||
		(!query.From.IsZero() && !query.To.IsZero() && query.To.Before(query.From)) ||
		query.Limit < 0 || query.Limit > containerlogs.MaximumLimit {
		return fmt.Errorf("%w: invalid telemetry log query", containerlogs.ErrInvalidQuery)
	}
	return nil
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
	return containerlogs.Record{
		Timestamp: time.Unix(0, int64(stored.TimeUnixNano)).UTC(), Stream: stream, Text: stored.Text,
		DeploymentID: stored.DeploymentID, AttemptID: stored.AttemptID, Partial: stored.Partial,
		TraceID: stored.TraceID, SpanID: stored.SpanID, SeverityText: stored.SeverityText,
		SeverityNumber: stored.SeverityNumber,
	}, nil
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
