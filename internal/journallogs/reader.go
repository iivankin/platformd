package journallogs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	journalctlPath        = "/usr/bin/journalctl"
	platformUnit          = "platformd.service"
	cloudflareMeshName    = "platformd-cloudflare-mesh"
	kernelIncidentPattern = `(?i)(out of memory|oom-kill|killed process|I/O error|filesystem error|EXT4-fs error|XFS.*error|BTRFS.*error|buffer I/O error|read-only file system|segfault|general protection fault)`
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
	if query.BeforeCursor != "" && !validCursor(query.BeforeCursor) {
		return Window{}, fmt.Errorf("%w: before cursor is invalid", ErrInvalidQuery)
	}
	commandContext, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	sources := []struct {
		name      string
		arguments []string
	}{
		{name: "platform", arguments: []string{"--unit=" + platformUnit}},
		{name: "cloudflare-mesh", arguments: []string{"CONTAINER_NAME=" + cloudflareMeshName}},
		{name: "kernel", arguments: []string{"--dmesg", "--grep=" + kernelIncidentPattern}},
		{name: "systemd", arguments: []string{"_PID=1", "--priority=0..4"}},
	}
	records := make([]Record, 0, query.Limit+1)
	outputTruncated := false
	seen := make(map[string]struct{}, query.Limit+1)
	for _, source := range sources {
		sourceRecords, sourceTruncated, err := reader.readSource(
			commandContext, source.name, source.arguments, query.Limit+1, query.BeforeCursor,
		)
		if err != nil {
			return Window{}, err
		}
		outputTruncated = outputTruncated || sourceTruncated
		for _, record := range sourceRecords {
			if _, exists := seen[record.Cursor]; exists {
				continue
			}
			seen[record.Cursor] = struct{}{}
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(left, right int) bool {
		return records[left].Timestamp.After(records[right].Timestamp)
	})
	hasMore := outputTruncated || len(records) > query.Limit
	if len(records) > query.Limit {
		records = records[:query.Limit]
	}
	window := Window{Records: records}
	if hasMore && len(records) != 0 {
		window.NextCursor = records[len(records)-1].Cursor
	}
	return window, nil
}

func (reader *Reader) readSource(
	ctx context.Context,
	name string,
	sourceArguments []string,
	limit int,
	beforeCursor string,
) ([]Record, bool, error) {
	stdout := &boundedBuffer{maximum: maximumOutputBytes}
	stderr := &boundedBuffer{maximum: maximumErrorBytes}
	arguments := append([]string(nil), sourceArguments...)
	if beforeCursor != "" {
		// With reverse traversal, journalctl advances to entries older than
		// the cursor and excludes the boundary entry when it is still present.
		arguments = append(arguments, "--after-cursor="+beforeCursor)
	}
	arguments = append(arguments,
		"--output=json",
		"--no-pager",
		"--reverse",
		"--lines="+strconv.Itoa(limit),
	)
	if err := reader.runner.Run(ctx, arguments, stdout, stderr); err != nil {
		if ctx.Err() != nil {
			return nil, false, fmt.Errorf("read %s journal: %w", name, ctx.Err())
		}
		if journalGrepHasNoMatches(err, arguments, stdout, stderr) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s journal: %w: %s", name, err, sanitizeMessage(stderr.String()))
	}
	records, err := parseOutput(stdout.Bytes(), stdout.truncated)
	if err != nil {
		return nil, false, err
	}
	return records, stdout.truncated, nil
}

func journalGrepHasNoMatches(err error, arguments []string, stdout, stderr *boundedBuffer) bool {
	var exitError interface{ ExitCode() int }
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 ||
		len(stdout.Bytes()) != 0 || len(stderr.Bytes()) != 0 {
		return false
	}
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "--grep=") {
			// journalctl uses status 1 for a successful grep with zero matches.
			return true
		}
	}
	return false
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
		return Record{}, fmt.Errorf("decode system journal record: %w", err)
	}
	timestampText, err := journalString(fields["__REALTIME_TIMESTAMP"])
	if err != nil {
		return Record{}, fmt.Errorf("decode system journal timestamp: %w", err)
	}
	timestampMicros, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return Record{}, fmt.Errorf("decode system journal timestamp: %w", err)
	}
	priorityText, err := journalString(fields["PRIORITY"])
	if err != nil {
		return Record{}, fmt.Errorf("decode system journal priority: %w", err)
	}
	priority, err := strconv.Atoi(priorityText)
	if err != nil || priority < 0 || priority > 7 {
		return Record{}, errors.New("decode system journal priority: value must be 0..7")
	}
	message, err := journalString(fields["MESSAGE"])
	if err != nil {
		return Record{}, fmt.Errorf("decode system journal message: %w", err)
	}
	priority = systemEventPriority(message, priority)
	cursor, err := journalString(fields["__CURSOR"])
	if err != nil || cursor == "" {
		return Record{}, errors.New("decode system journal cursor: value is required")
	}
	identifier, _ := journalString(fields["SYSLOG_IDENTIFIER"])
	if containerName, _ := journalString(fields["CONTAINER_NAME"]); containerName != "" {
		identifier = containerName
	}
	pid, _ := journalString(fields["_PID"])
	if !validCursor(cursor) {
		return Record{}, errors.New("decode system journal cursor: value is invalid")
	}
	return Record{
		Timestamp: time.UnixMicro(timestampMicros).UTC(), Priority: priority,
		Message: sanitizeMessage(message), Identifier: sanitizeLabel(identifier),
		PID: sanitizeLabel(pid), Cursor: cursor,
	}, nil
}

func validCursor(value string) bool {
	if value == "" || len(value) > maximumCursorBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func systemEventPriority(message string, fallback int) int {
	switch {
	case strings.HasPrefix(message, "level=error event="):
		return 3
	case strings.HasPrefix(message, "level=warning event="):
		return 4
	case strings.HasPrefix(message, "level=info event="):
		return 6
	default:
		return fallback
	}
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
