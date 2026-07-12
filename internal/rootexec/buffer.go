package rootexec

import (
	"sync"
)

type boundedBuffer struct {
	mu        sync.Mutex
	value     []byte
	limit     int
	truncated bool
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{value: make([]byte, 0, min(limit, 4096)), limit: limit}
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	remaining := buffer.limit - len(buffer.value)
	if remaining > 0 {
		buffer.value = append(buffer.value, value[:min(len(value), remaining)]...)
	}
	if len(value) > remaining {
		buffer.truncated = true
	}
	return len(value), nil
}

func (buffer *boundedBuffer) Result() (string, bool) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.value), buffer.truncated
}
