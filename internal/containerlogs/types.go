package containerlogs

import (
	"errors"
	"time"
)

var ErrInvalidQuery = errors.New("invalid container log query")

const (
	DefaultLimit         = 500
	MaximumLimit         = 2000
	DefaultScanBytes     = 4 << 20
	DefaultRecordBytes   = 64 << 10
	MaximumDownloadBytes = 100 << 20
	truncationMarker     = "… [truncated]"
	maximumContainsBytes = 256
)

const MaximumDownloadRange = 24 * time.Hour

type Query struct {
	ServiceID    string
	DeploymentID string
	Contains     string
	Limit        int
}

type Record struct {
	Timestamp    time.Time `json:"timestamp"`
	Stream       string    `json:"stream"`
	Text         string    `json:"text"`
	DeploymentID string    `json:"deploymentId"`
	AttemptID    string    `json:"attemptId"`
	Partial      bool      `json:"partial,omitempty"`
	Truncated    bool      `json:"truncated,omitempty"`

	segment int
	offset  int64
}

type Window struct {
	Records   []Record `json:"records"`
	Truncated bool     `json:"truncated"`
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
