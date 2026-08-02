package objectstore

import (
	"path/filepath"
	"strings"
)

const (
	Region               = "us-east-1"
	MaximumObjectSize    = int64(100) << 30
	streamingPayloadHash = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"
)

func safeComponent(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value && !strings.ContainsAny(value, "/\\\x00")
}
