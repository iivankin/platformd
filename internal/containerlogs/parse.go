package containerlogs

import (
	"bytes"
	"strings"
	"time"
	"unicode/utf8"
)

func parseRecords(data []byte, startOffset int64, partialHead bool, source segment, contains string, limit, maximumRecord int) ([]Record, bool) {
	lines := bytes.Split(data, []byte{'\n'})
	incompleteTail := len(data) != 0 && data[len(data)-1] != '\n'
	if incompleteTail && len(lines) != 0 {
		lines = lines[:len(lines)-1]
	}
	lineOffset := startOffset
	if partialHead && len(lines) != 0 {
		lineOffset += int64(len(lines[0]) + 1)
		lines = lines[1:]
	}
	result := make([]Record, 0, min(limit, len(lines)))
	dropped := incompleteTail
	for _, line := range lines {
		if len(line) == 0 {
			lineOffset++
			continue
		}
		fields := bytes.SplitN(line, []byte{' '}, 4)
		if len(fields) != 4 || (string(fields[1]) != "stdout" && string(fields[1]) != "stderr") || (string(fields[2]) != "F" && string(fields[2]) != "P") {
			lineOffset += int64(len(line) + 1)
			continue
		}
		timestamp, err := time.Parse(time.RFC3339Nano, string(fields[0]))
		if err != nil {
			lineOffset += int64(len(line) + 1)
			continue
		}
		text := sanitizeText(fields[3])
		truncated := false
		if len(text) > maximumRecord {
			text = truncateText(text, maximumRecord-len(truncationMarker)) + truncationMarker
			truncated = true
		}
		if contains == "" || strings.Contains(text, contains) {
			result = append(result, Record{
				Timestamp: timestamp, Stream: string(fields[1]), Text: text,
				DeploymentID: source.deploymentID, AttemptID: source.attemptID,
				Partial: string(fields[2]) == "P", Truncated: truncated,
				segment: source.rotation, offset: lineOffset,
			})
			if len(result) > limit {
				result = result[1:]
				dropped = true
			}
		}
		lineOffset += int64(len(line) + 1)
	}
	return result, dropped
}

func sanitizeText(value []byte) string {
	valid := strings.ToValidUTF8(string(value), "�")
	return strings.Map(func(character rune) rune {
		if character == '\t' || character >= 0x20 && character != 0x7f {
			return character
		}
		return '�'
	}, valid)
}

func truncateText(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
