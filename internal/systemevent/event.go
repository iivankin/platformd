package systemevent

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const maximumValueBytes = 2 << 10

var writeMu sync.Mutex

type lineWriter struct {
	mu     sync.Mutex
	event  string
	fields []Field
	buffer []byte
	closed bool
}

type Field struct {
	key   string
	value string
}

func String(key, value string) Field {
	return Field{key: sanitizeKey(key), value: sanitizeValue(value)}
}

func Int64(key string, value int64) Field {
	return Field{key: sanitizeKey(key), value: strconv.FormatInt(value, 10)}
}

func Uint64(key string, value uint64) Field {
	return Field{key: sanitizeKey(key), value: strconv.FormatUint(value, 10)}
}

func Error(cause error) Field {
	if cause == nil {
		return Field{}
	}
	return String("error", cause.Error())
}

func Info(event string, fields ...Field) {
	write("info", event, fields)
}

func Warning(event string, fields ...Field) {
	write("warning", event, fields)
}

func Failure(event string, cause error, fields ...Field) {
	write("error", event, append(fields, Error(cause)))
}

// NewLineWriter turns streaming command output into bounded structured journal
// records. Long unterminated lines are split so a noisy tool cannot grow
// platformd's heap while journald applies its normal retention and rate limits.
func NewLineWriter(event string, fields ...Field) io.WriteCloser {
	return &lineWriter{event: sanitizeKey(event), fields: append([]Field(nil), fields...)}
}

func (writer *lineWriter) Write(input []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return 0, io.ErrClosedPipe
	}
	requested := len(input)
	for len(input) != 0 {
		newline := bytes.IndexByte(input, '\n')
		segment := input
		if newline >= 0 {
			segment = input[:newline]
		}
		for len(segment) != 0 {
			remaining := maximumValueBytes - len(writer.buffer)
			count := min(remaining, len(segment))
			writer.buffer = append(writer.buffer, segment[:count]...)
			segment = segment[count:]
			if len(writer.buffer) == maximumValueBytes {
				writer.flush(true)
			}
		}
		if newline < 0 {
			break
		}
		writer.flush(false)
		input = input[newline+1:]
	}
	return requested, nil
}

func (writer *lineWriter) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return nil
	}
	writer.closed = true
	writer.flush(false)
	return nil
}

func (writer *lineWriter) flush(partial bool) {
	if len(writer.buffer) == 0 {
		return
	}
	fields := append([]Field(nil), writer.fields...)
	fields = append(fields, String("message", string(writer.buffer)))
	if partial {
		fields = append(fields, String("partial", "true"))
	}
	write("info", writer.event, fields)
	writer.buffer = writer.buffer[:0]
}

func write(level, event string, fields []Field) {
	var message strings.Builder
	message.WriteString("level=")
	message.WriteString(level)
	message.WriteString(" event=")
	message.WriteString(sanitizeKey(event))
	for _, field := range fields {
		if field.key == "" || field.value == "" {
			continue
		}
		message.WriteByte(' ')
		message.WriteString(field.key)
		message.WriteByte('=')
		message.WriteString(strconv.Quote(field.value))
	}
	// Journald already supplies timestamps. Keeping the structured prefix at the
	// start also lets the bounded reader map event levels to its priority column.
	writeMu.Lock()
	defer writeMu.Unlock()
	_, _ = fmt.Fprintln(log.Writer(), message.String())
}

func sanitizeKey(value string) string {
	value = strings.TrimSpace(value)
	var result strings.Builder
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '_' {
			result.WriteRune(unicode.ToLower(character))
		}
	}
	return result.String()
}

func sanitizeValue(value string) string {
	value = strings.ToValidUTF8(value, "�")
	value = strings.Map(func(character rune) rune {
		if character == '\n' || character == '\r' || character == '\t' || unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= maximumValueBytes {
		return value
	}
	value = value[:maximumValueBytes-len("…")]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "…"
}
