package managedredis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

const (
	DefaultScanCount    = 50
	MaximumScanCount    = 100
	DefaultPreviewCount = 20
	MaximumPreviewCount = 100
	MaximumPreviewBytes = 64 << 10
	maximumMatchBytes   = 256
)

var (
	ErrInvalidBrowserQuery = errors.New("invalid managed Redis browser query")
	ErrNotRunning          = errors.New("managed Redis resource is not running")
	ErrMaintenance         = errors.New("managed Redis resource is in maintenance")
	ErrKeyNotFound         = errors.New("managed Redis key not found")
)

type ScanQuery struct {
	Cursor uint64
	Match  string
	Count  int
}

type KeySummary struct {
	Key             []byte
	Type            string
	ExpiresInMillis *int64
	SizeBytes       int64
}

type KeyPage struct {
	NextCursor uint64
	Keys       []KeySummary
}

type PreviewQuery struct {
	Key   []byte
	Count int
}

type PreviewItem struct {
	Values [][]byte
}

type Preview struct {
	Type       string
	Length     int64
	NextCursor uint64
	Items      []PreviewItem
	Truncated  bool
}

func (client *Client) ScanKeys(ctx context.Context, query ScanQuery) (KeyPage, error) {
	if query.Count == 0 {
		query.Count = DefaultScanCount
	}
	if query.Count < 1 || query.Count > MaximumScanCount || len(query.Match) > maximumMatchBytes {
		return KeyPage{}, fmt.Errorf("%w: count must be 1..100 and match at most 256 bytes", ErrInvalidBrowserQuery)
	}
	arguments := []string{"SCAN", strconv.FormatUint(query.Cursor, 10)}
	if query.Match != "" {
		arguments = append(arguments, "MATCH", query.Match)
	}
	arguments = append(arguments, "COUNT", strconv.Itoa(query.Count))
	value, err := client.command(ctx, arguments...)
	if err != nil {
		return KeyPage{}, err
	}
	nextCursor, keys, err := parseScanResponse(value)
	if err != nil {
		return KeyPage{}, err
	}
	page := KeyPage{NextCursor: nextCursor, Keys: make([]KeySummary, 0, len(keys))}
	for _, key := range keys {
		summary, exists, err := client.keySummary(ctx, key)
		if err != nil {
			return KeyPage{}, err
		}
		if exists {
			page.Keys = append(page.Keys, summary)
		}
	}
	return page, nil
}

func parseScanResponse(value response) (uint64, [][]byte, error) {
	if value.kind != responseArray || len(value.array) != 2 || value.array[0].kind != responseBulk || value.array[1].kind != responseArray {
		return 0, nil, errors.New("Redis SCAN response has an unexpected shape")
	}
	cursor, err := strconv.ParseUint(string(value.array[0].bulk), 10, 64)
	if err != nil {
		return 0, nil, errors.New("Redis SCAN cursor is invalid")
	}
	keys := make([][]byte, 0, len(value.array[1].array))
	for _, item := range value.array[1].array {
		if item.kind != responseBulk {
			return 0, nil, errors.New("Redis SCAN key is not a bulk string")
		}
		keys = append(keys, append([]byte(nil), item.bulk...))
	}
	return cursor, keys, nil
}

func (client *Client) keySummary(ctx context.Context, key []byte) (KeySummary, bool, error) {
	typeResponse, err := client.command(ctx, "TYPE", string(key))
	if err != nil {
		return KeySummary{}, false, err
	}
	if typeResponse.kind != responseString {
		return KeySummary{}, false, errors.New("Redis TYPE response has an unexpected shape")
	}
	if typeResponse.text == "none" {
		return KeySummary{}, false, nil
	}
	ttlResponse, err := client.command(ctx, "PTTL", string(key))
	if err != nil {
		return KeySummary{}, false, err
	}
	if ttlResponse.kind != responseInteger {
		return KeySummary{}, false, errors.New("Redis PTTL response has an unexpected shape")
	}
	if ttlResponse.integer == -2 {
		return KeySummary{}, false, nil
	}
	if ttlResponse.integer < -2 {
		return KeySummary{}, false, errors.New("Redis PTTL response is invalid")
	}
	sizeResponse, err := client.command(ctx, "MEMORY", "USAGE", string(key))
	if err != nil {
		return KeySummary{}, false, err
	}
	size := int64(0)
	if sizeResponse.kind == responseInteger {
		if sizeResponse.integer < 0 {
			return KeySummary{}, false, errors.New("Redis MEMORY USAGE response is negative")
		}
		size = sizeResponse.integer
	} else if sizeResponse.kind != responseNull {
		return KeySummary{}, false, errors.New("Redis MEMORY USAGE response has an unexpected shape")
	}
	var expires *int64
	if ttlResponse.integer >= 0 {
		value := ttlResponse.integer
		expires = &value
	}
	return KeySummary{Key: append([]byte(nil), key...), Type: typeResponse.text, ExpiresInMillis: expires, SizeBytes: size}, true, nil
}

func (client *Client) PreviewKey(ctx context.Context, query PreviewQuery) (Preview, error) {
	if query.Count == 0 {
		query.Count = DefaultPreviewCount
	}
	if len(query.Key) > maximumCommandBytes || query.Count < 1 || query.Count > MaximumPreviewCount {
		return Preview{}, fmt.Errorf("%w: key must be at most 1 MiB and count 1..100", ErrInvalidBrowserQuery)
	}
	typeResponse, err := client.command(ctx, "TYPE", string(query.Key))
	if err != nil {
		return Preview{}, err
	}
	if typeResponse.kind != responseString {
		return Preview{}, errors.New("Redis TYPE response has an unexpected shape")
	}
	if typeResponse.text == "none" {
		return Preview{}, ErrKeyNotFound
	}
	preview := Preview{Type: typeResponse.text, Items: make([]PreviewItem, 0, query.Count)}
	switch typeResponse.text {
	case "string":
		preview.Length, err = client.nonnegativeInteger(ctx, "STRLEN", string(query.Key))
		if err != nil {
			return Preview{}, err
		}
		value, commandErr := client.command(ctx, "GETRANGE", string(query.Key), "0", strconv.Itoa(MaximumPreviewBytes-1))
		if commandErr != nil {
			return Preview{}, commandErr
		}
		if value.kind != responseBulk {
			return Preview{}, errors.New("Redis GETRANGE response has an unexpected shape")
		}
		preview.Items = append(preview.Items, PreviewItem{Values: [][]byte{append([]byte(nil), value.bulk...)}})
		preview.Truncated = preview.Length > int64(len(value.bulk))
	case "list":
		preview.Length, err = client.nonnegativeInteger(ctx, "LLEN", string(query.Key))
		if err != nil {
			return Preview{}, err
		}
		values, commandErr := client.bulkArray(ctx, "LRANGE", string(query.Key), "0", strconv.Itoa(query.Count-1))
		if commandErr != nil {
			return Preview{}, commandErr
		}
		preview.Items = singleValueItems(values)
		preview.Truncated = preview.Length > int64(len(values))
	case "set":
		preview.Length, err = client.nonnegativeInteger(ctx, "SCARD", string(query.Key))
		if err == nil {
			preview.NextCursor, preview.Items, err = client.scanPreview(ctx, "SSCAN", query.Key, query.Count, 1)
		}
		preview.Truncated = preview.NextCursor != 0
	case "hash":
		preview.Length, err = client.nonnegativeInteger(ctx, "HLEN", string(query.Key))
		if err == nil {
			preview.NextCursor, preview.Items, err = client.scanPreview(ctx, "HSCAN", query.Key, query.Count, 2)
		}
		preview.Truncated = preview.NextCursor != 0
	case "zset":
		preview.Length, err = client.nonnegativeInteger(ctx, "ZCARD", string(query.Key))
		if err == nil {
			preview.NextCursor, preview.Items, err = client.scanPreview(ctx, "ZSCAN", query.Key, query.Count, 2)
		}
		preview.Truncated = preview.NextCursor != 0
	case "stream":
		preview.Length, err = client.nonnegativeInteger(ctx, "XLEN", string(query.Key))
		if err != nil {
			return Preview{}, err
		}
		value, commandErr := client.command(ctx, "XRANGE", string(query.Key), "-", "+", "COUNT", strconv.Itoa(query.Count))
		if commandErr != nil {
			return Preview{}, commandErr
		}
		preview.Items, err = parseStreamItems(value)
		preview.Truncated = preview.Length > int64(len(preview.Items))
	default:
		return Preview{}, fmt.Errorf("%w: Redis type %q is not supported", ErrInvalidBrowserQuery, typeResponse.text)
	}
	if err != nil {
		return Preview{}, err
	}
	var bytesTruncated bool
	preview.Items, bytesTruncated = boundPreviewItems(preview.Items, MaximumPreviewBytes)
	preview.Truncated = preview.Truncated || bytesTruncated
	return preview, nil
}

func (client *Client) nonnegativeInteger(ctx context.Context, arguments ...string) (int64, error) {
	value, err := client.command(ctx, arguments...)
	if err != nil {
		return 0, err
	}
	if value.kind != responseInteger || value.integer < 0 {
		return 0, errors.New("Redis cardinality response is invalid")
	}
	return value.integer, nil
}

func (client *Client) bulkArray(ctx context.Context, arguments ...string) ([][]byte, error) {
	value, err := client.command(ctx, arguments...)
	if err != nil {
		return nil, err
	}
	if value.kind != responseArray {
		return nil, errors.New("Redis array response has an unexpected shape")
	}
	result := make([][]byte, 0, len(value.array))
	for _, item := range value.array {
		if item.kind != responseBulk {
			return nil, errors.New("Redis array item is not a bulk string")
		}
		result = append(result, append([]byte(nil), item.bulk...))
	}
	return result, nil
}

func (client *Client) scanPreview(ctx context.Context, command string, key []byte, count, width int) (uint64, []PreviewItem, error) {
	value, err := client.command(ctx, command, string(key), "0", "COUNT", strconv.Itoa(count))
	if err != nil {
		return 0, nil, err
	}
	cursor, values, err := parseScanResponse(value)
	if err != nil {
		return 0, nil, err
	}
	if len(values)%width != 0 {
		return 0, nil, errors.New("Redis collection scan returned an incomplete item")
	}
	items := make([]PreviewItem, 0, len(values)/width)
	for index := 0; index < len(values); index += width {
		items = append(items, PreviewItem{Values: values[index : index+width]})
	}
	return cursor, items, nil
}

func singleValueItems(values [][]byte) []PreviewItem {
	items := make([]PreviewItem, 0, len(values))
	for _, value := range values {
		items = append(items, PreviewItem{Values: [][]byte{value}})
	}
	return items
}

func parseStreamItems(value response) ([]PreviewItem, error) {
	if value.kind != responseArray {
		return nil, errors.New("Redis XRANGE response has an unexpected shape")
	}
	items := make([]PreviewItem, 0, len(value.array))
	for _, entry := range value.array {
		if entry.kind != responseArray || len(entry.array) != 2 || entry.array[0].kind != responseBulk || entry.array[1].kind != responseArray || len(entry.array[1].array)%2 != 0 {
			return nil, errors.New("Redis stream entry has an unexpected shape")
		}
		values := make([][]byte, 0, 1+len(entry.array[1].array))
		values = append(values, append([]byte(nil), entry.array[0].bulk...))
		for _, field := range entry.array[1].array {
			if field.kind != responseBulk {
				return nil, errors.New("Redis stream field is not a bulk string")
			}
			values = append(values, append([]byte(nil), field.bulk...))
		}
		items = append(items, PreviewItem{Values: values})
	}
	return items, nil
}

func boundPreviewItems(items []PreviewItem, maximum int) ([]PreviewItem, bool) {
	result := make([]PreviewItem, 0, len(items))
	remaining := maximum
	for _, item := range items {
		itemBytes := 0
		for _, value := range item.Values {
			itemBytes += len(value)
		}
		// Collection items remain atomic so the UI never offers a destructive
		// action for a field/member/stream ID truncated by the preview byte cap.
		if itemBytes > remaining {
			return result, true
		}
		bounded := PreviewItem{Values: make([][]byte, 0, len(item.Values))}
		for _, value := range item.Values {
			bounded.Values = append(bounded.Values, append([]byte(nil), value...))
		}
		remaining -= itemBytes
		result = append(result, bounded)
	}
	return result, false
}
