package containerlogs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

const (
	downloadLineBufferBytes = DefaultRecordBytes + 512
	downloadFooterReserve   = 256
)

type downloadHeader struct {
	Type         string    `json:"type"`
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	ServiceID    string    `json:"serviceId"`
	DeploymentID string    `json:"deploymentId,omitempty"`
}

type downloadRecord struct {
	Type string `json:"type"`
	Record
}

type downloadFooter struct {
	Type      string `json:"type"`
	Records   int    `json:"records"`
	Truncated bool   `json:"truncated"`
}

func (reader *Reader) Download(ctx context.Context, query DownloadQuery, destination io.Writer) (DownloadResult, error) {
	if destination == nil {
		return DownloadResult{}, errors.New("container log download destination is required")
	}
	if err := validateDownloadQuery(query); err != nil {
		return DownloadResult{}, err
	}
	segments, err := reader.segments(Query{
		ServiceID: query.ServiceID, DeploymentID: query.DeploymentID, Limit: 1,
	})
	if err != nil {
		return DownloadResult{}, err
	}
	sortDownloadSegments(segments)
	writer := &downloadWriter{destination: destination, dataLimit: MaximumDownloadBytes - downloadFooterReserve}
	if err := writer.line(downloadHeader{
		Type: "platformd.log_export", From: query.From, To: query.To,
		ServiceID: query.ServiceID, DeploymentID: query.DeploymentID,
	}); err != nil {
		return writer.result, err
	}
	for _, current := range segments {
		if err := ctx.Err(); err != nil {
			return writer.result, err
		}
		file, err := os.Open(current.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return writer.result, fmt.Errorf("open container log download segment: %w", err)
		}
		stop, readErr := downloadSegment(ctx, file, current, query, reader.maximumRecord, writer)
		closeErr := file.Close()
		if readErr != nil {
			return writer.result, readErr
		}
		if closeErr != nil {
			return writer.result, fmt.Errorf("close container log download segment: %w", closeErr)
		}
		if stop {
			writer.result.Truncated = true
			break
		}
	}
	writer.dataLimit = MaximumDownloadBytes
	if err := writer.line(downloadFooter{
		Type: "platformd.log_export_complete", Records: writer.result.Records,
		Truncated: writer.result.Truncated,
	}); err != nil {
		return writer.result, err
	}
	return writer.result, nil
}

func validateDownloadQuery(query DownloadQuery) error {
	if !productID.MatchString(query.ServiceID) || (query.DeploymentID != "" && !productID.MatchString(query.DeploymentID)) {
		return fmt.Errorf("%w: invalid service or deployment ID", ErrInvalidQuery)
	}
	if query.From.IsZero() || query.To.IsZero() || !query.To.After(query.From) || query.To.Sub(query.From) > MaximumDownloadRange {
		return fmt.Errorf("%w: download range must be greater than zero and at most 24 hours", ErrInvalidQuery)
	}
	return nil
}

func sortDownloadSegments(segments []segment) {
	sort.Slice(segments, func(left, right int) bool {
		if segments[left].modifiedNano != segments[right].modifiedNano {
			return segments[left].modifiedNano < segments[right].modifiedNano
		}
		if segments[left].deploymentID != segments[right].deploymentID {
			return segments[left].deploymentID < segments[right].deploymentID
		}
		if segments[left].attemptID != segments[right].attemptID {
			return segments[left].attemptID < segments[right].attemptID
		}
		if segments[left].rotation != segments[right].rotation {
			return segments[left].rotation > segments[right].rotation
		}
		return segments[left].path < segments[right].path
	})
}

func downloadSegment(
	ctx context.Context,
	file io.Reader,
	source segment,
	query DownloadQuery,
	maximumRecord int,
	writer *downloadWriter,
) (bool, error) {
	lines := bufio.NewReaderSize(file, downloadLineBufferBytes)
	var offset int64
	for {
		line, consumed, oversized, complete, err := readDownloadLine(lines, downloadLineBufferBytes)
		if complete && len(line) != 0 {
			record, valid := parseRecordLine(line, source, offset, maximumRecord, oversized)
			if valid && !record.Timestamp.Before(query.From) && !record.Timestamp.After(query.To) {
				if err := writer.record(record); errors.Is(err, errDownloadLimit) {
					return true, nil
				} else if err != nil {
					return false, err
				}
			}
		}
		offset += consumed
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, fmt.Errorf("read container log download segment: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
}

func readDownloadLine(reader *bufio.Reader, retain int) ([]byte, int64, bool, bool, error) {
	var result []byte
	var consumed int64
	oversized := false
	for {
		fragment, err := reader.ReadSlice('\n')
		consumed += int64(len(fragment))
		remaining := retain - len(result)
		if remaining > 0 {
			result = append(result, fragment[:min(remaining, len(fragment))]...)
		}
		if len(fragment) > remaining || errors.Is(err, bufio.ErrBufferFull) {
			oversized = true
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		complete := len(fragment) != 0 && fragment[len(fragment)-1] == '\n'
		result = bytes.TrimSuffix(result, []byte{'\n'})
		return result, consumed, oversized, complete, err
	}
}

var errDownloadLimit = errors.New("container log download byte limit reached")

type downloadWriter struct {
	destination io.Writer
	dataLimit   int64
	result      DownloadResult
}

func (writer *downloadWriter) record(record Record) error {
	if err := writer.line(downloadRecord{Type: "record", Record: record}); err != nil {
		return err
	}
	writer.result.Records++
	return nil
}

func (writer *downloadWriter) line(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode container log download: %w", err)
	}
	payload = append(payload, '\n')
	if writer.result.Bytes+int64(len(payload)) > writer.dataLimit {
		return errDownloadLimit
	}
	written, err := writer.destination.Write(payload)
	writer.result.Bytes += int64(written)
	if err != nil {
		return fmt.Errorf("write container log download: %w", err)
	}
	if written != len(payload) {
		return io.ErrShortWrite
	}
	return nil
}
