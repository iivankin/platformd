package telemetry

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	logQueueCapacity         = 4096
	logBatchSize             = 256
	maximumLogBytes          = 64 << 10
	containerAttachScope     = "platformd.container.attach"
	beforeDeployCommandScope = "platformd.before_deploy"
)

type LogMetadata struct {
	ServiceID    string
	ServiceName  string
	DeploymentID string
	AttemptID    string
	Stream       string
	ScopeName    string
}

type logRecord struct {
	metadata LogMetadata
	body     []byte
	time     time.Time
	partial  bool
}

type LogExporter struct {
	endpoint string
	client   *http.Client
	queue    chan logRecord
	cancel   context.CancelFunc
	done     chan struct{}
	closed   atomic.Bool
	dropped  atomic.Uint64
}

func NewLogExporter(parent context.Context, endpoint string) *LogExporter {
	ctx, cancel := context.WithCancel(parent)
	exporter := &LogExporter{
		endpoint: endpoint,
		client:   &http.Client{Timeout: 2 * time.Second},
		queue:    make(chan logRecord, logQueueCapacity),
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	go exporter.run(ctx)
	return exporter
}

func (exporter *LogExporter) Writer(metadata LogMetadata) io.WriteCloser {
	return &lineWriter{exporter: exporter, metadata: metadata}
}

func (exporter *LogExporter) ContainerWriter(serviceID, serviceName, deploymentID, attemptID, stream string) io.WriteCloser {
	return exporter.Writer(LogMetadata{
		ServiceID: serviceID, ServiceName: serviceName,
		DeploymentID: deploymentID, AttemptID: attemptID, Stream: stream,
		ScopeName: containerAttachScope,
	})
}

func (exporter *LogExporter) BeforeDeployWriter(serviceID, serviceName, deploymentID, attemptID, stream string) io.WriteCloser {
	return exporter.Writer(LogMetadata{
		ServiceID: serviceID, ServiceName: serviceName,
		DeploymentID: deploymentID, AttemptID: attemptID, Stream: stream,
		ScopeName: beforeDeployCommandScope,
	})
}

func (exporter *LogExporter) Close() {
	if !exporter.closed.CompareAndSwap(false, true) {
		return
	}
	exporter.cancel()
	<-exporter.done
}

func (exporter *LogExporter) enqueue(record logRecord) {
	if exporter.closed.Load() {
		exporter.dropped.Add(1)
		return
	}
	select {
	case exporter.queue <- record:
	default:
		exporter.dropped.Add(1)
	}
}

func (exporter *LogExporter) run(ctx context.Context) {
	defer close(exporter.done)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	batch := make([]logRecord, 0, logBatchSize)
	for {
		select {
		case <-ctx.Done():
			exporter.flushOnClose(batch)
			return
		case record := <-exporter.queue:
			batch = append(batch, record)
			if len(batch) >= logBatchSize && exporter.retry(ctx, batch) {
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 && exporter.retry(ctx, batch) {
				batch = batch[:0]
			}
		}
	}
}

func (exporter *LogExporter) flushOnClose(batch []logRecord) {
	for {
		exporter.drain(&batch)
		if len(batch) == 0 {
			return
		}
		if !exporter.sendLogs(context.Background(), batch) {
			exporter.dropped.Add(uint64(len(batch)))
			return
		}
		batch = batch[:0]
	}
}

func (exporter *LogExporter) drain(batch *[]logRecord) {
	for len(*batch) < logBatchSize {
		select {
		case record := <-exporter.queue:
			*batch = append(*batch, record)
		default:
			return
		}
	}
}

func (exporter *LogExporter) retry(ctx context.Context, batch []logRecord) bool {
	delay := 100 * time.Millisecond
	for {
		if exporter.sendLogs(ctx, batch) {
			exporter.sendDroppedMetric(ctx)
			return true
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
		delay = min(delay*2, 5*time.Second)
	}
}

func (exporter *LogExporter) sendLogs(ctx context.Context, records []logRecord) bool {
	if len(records) == 0 {
		return true
	}
	groups := make(map[LogMetadata][]map[string]any)
	for _, record := range records {
		body := map[string]any{}
		if utf8.Valid(record.body) {
			body["stringValue"] = string(record.body)
		} else {
			body["bytesValue"] = base64.StdEncoding.EncodeToString(record.body)
		}
		attributes := []any{attribute("log.iostream", record.metadata.Stream)}
		if record.partial {
			attributes = append(attributes, map[string]any{
				"key": "platformd.log.partial", "value": map[string]any{"boolValue": true},
			})
		}
		groups[record.metadata] = append(groups[record.metadata], map[string]any{
			"timeUnixNano":         strconv.FormatInt(record.time.UnixNano(), 10),
			"observedTimeUnixNano": strconv.FormatInt(time.Now().UnixNano(), 10),
			"body":                 body,
			"attributes":           attributes,
		})
	}
	resourceLogs := make([]any, 0, len(groups))
	for metadata, logRecords := range groups {
		scopeName := metadata.ScopeName
		if scopeName == "" {
			scopeName = containerAttachScope
		}
		resourceLogs = append(resourceLogs, map[string]any{
			"resource": map[string]any{"attributes": []any{
				attribute("service.id", metadata.ServiceID),
				attribute("service.name", metadata.ServiceName),
				attribute("platformd.deployment.id", metadata.DeploymentID),
				attribute("service.instance.id", metadata.AttemptID),
			}},
			"scopeLogs": []any{map[string]any{
				"scope":      map[string]any{"name": scopeName, "version": "1"},
				"logRecords": logRecords,
			}},
		})
	}
	return exporter.post(ctx, "/v1/logs", map[string]any{"resourceLogs": resourceLogs})
}

func (exporter *LogExporter) sendDroppedMetric(ctx context.Context) {
	dropped := exporter.dropped.Load()
	if dropped == 0 {
		return
	}
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	payload := map[string]any{"resourceMetrics": []any{map[string]any{
		"resource": map[string]any{"attributes": []any{attribute("service.name", "platformd")}},
		"scopeMetrics": []any{map[string]any{
			"scope": map[string]any{"name": "platformd.telemetry", "version": "1"},
			"metrics": []any{map[string]any{
				"name":        "platformd.telemetry.container_logs.dropped",
				"description": "Container log records dropped because the local OTLP queue was full",
				"unit":        "{record}",
				"gauge": map[string]any{"dataPoints": []any{map[string]any{
					"timeUnixNano": now, "asInt": strconv.FormatUint(dropped, 10),
				}}},
			}},
		}},
	}}}
	exporter.post(ctx, "/v1/metrics", payload)
}

func (exporter *LogExporter) post(ctx context.Context, path string, payload any) bool {
	body, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, exporter.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return false
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := exporter.client.Do(request)
	if err != nil {
		return false
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 300
}

func attribute(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}

type lineWriter struct {
	mu       sync.Mutex
	exporter *LogExporter
	metadata LogMetadata
	buffer   []byte
	closed   bool
	partial  bool
}

func (writer *lineWriter) Write(input []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return 0, io.ErrClosedPipe
	}
	written := len(input)
	writer.buffer = append(writer.buffer, input...)
	for {
		index := bytes.IndexByte(writer.buffer, '\n')
		if index < 0 && len(writer.buffer) < maximumLogBytes {
			break
		}
		if index >= 0 {
			line := writer.buffer[:index]
			linePartial := writer.partial || len(line) > maximumLogBytes
			for len(line) > maximumLogBytes {
				writer.emit(line[:maximumLogBytes], true)
				line = line[maximumLogBytes:]
			}
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			// A line whose size is an exact multiple of the chunk limit has
			// already been emitted when its trailing newline arrives.
			if len(line) > 0 || !writer.partial {
				writer.emit(line, linePartial)
			}
			writer.buffer = writer.buffer[index+1:]
			writer.partial = false
			continue
		}
		writer.emit(writer.buffer[:maximumLogBytes], true)
		writer.buffer = writer.buffer[maximumLogBytes:]
		writer.partial = true
	}
	return written, nil
}

func (writer *lineWriter) emit(body []byte, partial bool) {
	writer.exporter.enqueue(logRecord{
		metadata: writer.metadata, body: append([]byte(nil), body...), time: time.Now(), partial: partial,
	})
}

func (writer *lineWriter) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return nil
	}
	writer.closed = true
	if len(writer.buffer) > 0 {
		writer.emit(writer.buffer, writer.partial)
	}
	writer.buffer = nil
	return nil
}
