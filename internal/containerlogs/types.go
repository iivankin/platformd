package containerlogs

import (
	"errors"
	"time"
)

var ErrInvalidQuery = errors.New("invalid container log query")

const (
	DefaultLimit            = 500
	MaximumLimit            = 2000
	MaximumFieldFilters     = 8
	MaximumFieldFilterBytes = 8 << 10
	MaximumDownloadBytes    = 100 << 20
	MaximumContainsBytes    = 256
)

type FieldFilter struct {
	Path     string `json:"path"`
	Operator string `json:"operator"`
	Value    string `json:"value,omitempty"`
}

const MaximumDownloadRange = 24 * time.Hour

type Query struct {
	ServiceID    string
	ServiceIDs   []string
	DeploymentID string
	Contains     string
	Cursor       string
	FieldFilters []FieldFilter
	SeverityText string
	TraceID      string
	SpanID       string
	From         time.Time
	To           time.Time
	Limit        int
	Ascending    bool
}

type ResourceQuery struct {
	Kind       string
	ResourceID string
	Query
}

type Record struct {
	ServiceID      string         `json:"serviceId,omitempty"`
	Timestamp      time.Time      `json:"timestamp"`
	Stream         string         `json:"stream"`
	Text           string         `json:"text"`
	DeploymentID   string         `json:"deploymentId"`
	AttemptID      string         `json:"attemptId"`
	TraceID        string         `json:"traceId,omitempty"`
	SpanID         string         `json:"spanId,omitempty"`
	SeverityText   string         `json:"severityText,omitempty"`
	SeverityNumber int32          `json:"severityNumber,omitempty"`
	Phase          string         `json:"phase,omitempty"`
	Partial        bool           `json:"partial,omitempty"`
	Truncated      bool           `json:"truncated,omitempty"`
	Fields         map[string]any `json:"fields,omitempty"`
}

type Window struct {
	Records    []Record `json:"records"`
	Truncated  bool     `json:"truncated"`
	NextCursor string   `json:"nextCursor,omitempty"`
}

type DownloadQuery struct {
	ServiceID    string
	DeploymentID string
	From         time.Time
	To           time.Time
}

type DownloadResult struct {
	Bytes     int64
	Records   int
	Truncated bool
}
