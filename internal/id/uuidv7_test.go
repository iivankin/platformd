package id_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/id"
)

func TestUUIDv7Layout(t *testing.T) {
	t.Parallel()

	timestamp := time.UnixMilli(1_700_000_000_123)
	value, err := id.NewWith(timestamp, bytes.NewReader(bytes.Repeat([]byte{0xff}, 10)))
	if err != nil {
		t.Fatal(err)
	}
	if value != "018bcfe5-687b-7fff-bfff-ffffffffffff" {
		t.Fatalf("UUIDv7 = %s", value)
	}
}
