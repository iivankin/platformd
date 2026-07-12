package id

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

func New() (string, error) {
	return NewWith(time.Now(), rand.Reader)
}

func NewWith(timestamp time.Time, random io.Reader) (string, error) {
	var value [16]byte
	milliseconds := timestamp.UnixMilli()
	if milliseconds < 0 || milliseconds > 1<<48-1 {
		return "", fmt.Errorf("UUIDv7 timestamp %d is out of range", milliseconds)
	}
	binary.BigEndian.PutUint64(value[:8], uint64(milliseconds)<<16)
	if _, err := io.ReadFull(random, value[6:]); err != nil {
		return "", fmt.Errorf("generate UUIDv7 randomness: %w", err)
	}
	value[6] = value[6]&0x0f | 0x70
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	), nil
}
