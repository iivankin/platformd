package journallogs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	journalctlPath = "/usr/bin/journalctl"
	platformUnit   = "platformd.service"
)

type Runner interface {
	Run(context.Context, []string, io.Writer, io.Writer) error
}

type Reader struct {
	runner Runner
}

func NewReader() *Reader {
	return &Reader{runner: commandRunner{path: journalctlPath}}
}

func NewReaderWithRunner(runner Runner) (*Reader, error) {
	if runner == nil {
		return nil, errors.New("journal log runner is required")
	}
	return &Reader{runner: runner}, nil
}

func (reader *Reader) Read(ctx context.Context, query Query) (Window, error) {
	if query.Limit == 0 {
		query.Limit = DefaultLimit
	}
	if query.Limit < 1 || query.Limit > MaximumLimit {
		return Window{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidQuery, MaximumLimit)
	}
	commandContext, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	stdout := &boundedBuffer{maximum: maximumOutputBytes}
	stderr := &boundedBuffer{maximum: maximumErrorBytes}
	err := reader.runner.Run(commandContext, []string{
		"--unit=" + platformUnit,
		"--output=json",
		"--no-pager",
		"--reverse",
		"--lines=" + strconv.Itoa(query.Limit+1),
	}, stdout, stderr)
	if err != nil {
		if commandContext.Err() != nil {
			return Window{}, fmt.Errorf("read platform journal: %w", commandContext.Err())
		}
		return Window{}, fmt.Errorf("read platform journal: %w: %s", err, sanitizeMessage(stderr.String()))
	}
	records, err := parseOutput(stdout.Bytes(), stdout.truncated)
	if err != nil {
		return Window{}, err
	}
	truncated := stdout.truncated || len(records) > query.Limit
	if len(records) > query.Limit {
		records = records[:query.Limit]
	}
	return Window{Records: records, Truncated: truncated}, nil
}

type commandRunner struct {
	path string
}

func (runner commandRunner) Run(ctx context.Context, arguments []string, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, runner.path, arguments...)
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	maximum   int
	truncated bool
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	written := len(value)
	remaining := buffer.maximum - buffer.buffer.Len()
	if remaining > 0 {
		_, _ = buffer.buffer.Write(value[:min(remaining, len(value))])
	}
	if len(value) > remaining {
		buffer.truncated = true
	}
	return written, nil
}

func (buffer *boundedBuffer) Bytes() []byte {
	return buffer.buffer.Bytes()
}

func (buffer *boundedBuffer) String() string {
	return buffer.buffer.String()
}

func parseOutput(output []byte, truncated bool) ([]Record, error) {
	lines := bytes.Split(output, []byte{'\n'})
	if truncated && len(lines) != 0 {
		lines = lines[:len(lines)-1]
	}
	records := make([]Record, 0, len(lines))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		record, err := parseRecord(line)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func parseRecord(line []byte) (Record, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(line, &fields); err != nil {
		return Record{}, fmt.Errorf("decode platform journal record: %w", err)
	}
	timestampText, err := journalString(fields["__REALTIME_TIMESTAMP"])
	if err != nil {
		return Record{}, fmt.Errorf("decode platform journal timestamp: %w", err)
	}
	timestampMicros, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return Record{}, fmt.Errorf("decode platform journal timestamp: %w", err)
	}
	priorityText, err := journalString(fields["PRIORITY"])
	if err != nil {
		return Record{}, fmt.Errorf("decode platform journal priority: %w", err)
	}
	priority, err := strconv.Atoi(priorityText)
	if err != nil || priority < 0 || priority > 7 {
		return Record{}, errors.New("decode platform journal priority: value must be 0..7")
	}
	message, err := journalString(fields["MESSAGE"])
	if err != nil {
		return Record{}, fmt.Errorf("decode platform journal message: %w", err)
	}
	cursor, err := journalString(fields["__CURSOR"])
	if err != nil || cursor == "" {
		return Record{}, errors.New("decode platform journal cursor: value is required")
	}
	identifier, _ := journalString(fields["SYSLOG_IDENTIFIER"])
	pid, _ := journalString(fields["_PID"])
	return Record{
		Timestamp: time.UnixMicro(timestampMicros).UTC(), Priority: priority,
		Message: sanitizeMessage(message), Identifier: sanitizeLabel(identifier),
		PID: sanitizeLabel(pid), Cursor: sanitizeLabel(cursor),
	}, nil
}

func journalString(value json.RawMessage) (string, error) {
	if len(value) == 0 {
		return "", errors.New("field is missing")
	}
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return text, nil
	}
	var octets []byte
	if err := json.Unmarshal(value, &octets); err == nil {
		return string(octets), nil
	}
	return "", errors.New("field is not a string or byte array")
}

func sanitizeMessage(value string) string {
	value = strings.ToValidUTF8(value, "�")
	value = strings.Map(func(character rune) rune {
		if character == '\t' || character == '\n' || (character >= 0x20 && character != 0x7f) {
			return character
		}
		return '�'
	}, value)
	if len(value) <= maximumMessageBytes {
		return value
	}
	value = value[:maximumMessageBytes-len("… [truncated]")]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "… [truncated]"
}

func sanitizeLabel(value string) string {
	value = sanitizeMessage(value)
	if len(value) > 256 {
		value = value[:256]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}
