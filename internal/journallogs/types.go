package journallogs

import (
	"errors"
	"time"
)

var ErrInvalidQuery = errors.New("invalid journal log query")

const (
	DefaultLimit        = 500
	MaximumLimit        = 2000
	maximumOutputBytes  = 4 << 20
	maximumErrorBytes   = 64 << 10
	maximumMessageBytes = 64 << 10
)

const commandTimeout = 5 * time.Second

type Query struct {
	Limit int
}

type Record struct {
	Timestamp  time.Time `json:"timestamp"`
	Priority   int       `json:"priority"`
	Message    string    `json:"message"`
	Identifier string    `json:"identifier,omitempty"`
	PID        string    `json:"pid,omitempty"`
	Cursor     string    `json:"cursor"`
}

type Window struct {
	Records   []Record `json:"records"`
	Truncated bool     `json:"truncated"`
}
