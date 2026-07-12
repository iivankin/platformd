package containerlogs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var productID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var segmentName = regexp.MustCompile(`^([A-Za-z0-9_-]{1,128})\.log(?:\.([1-9][0-9]*))?$`)

type Reader struct {
	root          string
	maximumScan   int64
	maximumRecord int
}

type segment struct {
	path         string
	deploymentID string
	attemptID    string
	rotation     int
	modifiedNano int64
}

func NewReader(root string) (*Reader, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("container log root must be absolute")
	}
	return &Reader{root: filepath.Clean(root), maximumScan: DefaultScanBytes, maximumRecord: DefaultRecordBytes}, nil
}

func (reader *Reader) Read(ctx context.Context, query Query) (Window, error) {
	if !productID.MatchString(query.ServiceID) || (query.DeploymentID != "" && !productID.MatchString(query.DeploymentID)) {
		return Window{}, fmt.Errorf("%w: invalid service or deployment ID", ErrInvalidQuery)
	}
	if len(query.Contains) > maximumContainsBytes || strings.ContainsRune(query.Contains, '\x00') {
		return Window{}, fmt.Errorf("%w: contains filter exceeds its limit or contains NUL", ErrInvalidQuery)
	}
	if query.Limit == 0 {
		query.Limit = DefaultLimit
	}
	if query.Limit < 1 || query.Limit > MaximumLimit {
		return Window{}, fmt.Errorf("%w: log limit must be between 1 and %d", ErrInvalidQuery, MaximumLimit)
	}
	segments, err := reader.segments(query)
	if err != nil {
		return Window{}, err
	}
	remaining := reader.maximumScan
	records := make([]Record, 0, query.Limit)
	truncated := false
	for index, current := range segments {
		if err := ctx.Err(); err != nil {
			return Window{}, err
		}
		if remaining == 0 || len(records) >= query.Limit {
			truncated = truncated || index < len(segments)
			break
		}
		data, startOffset, consumed, partialHead, readErr := readSegmentTail(current.path, remaining)
		remaining -= consumed
		if readErr != nil {
			return Window{}, fmt.Errorf("read container log segment: %w", readErr)
		}
		parsed, dropped := parseRecords(data, startOffset, partialHead, current, query.Contains, query.Limit-len(records), reader.maximumRecord)
		records = append(records, parsed...)
		truncated = truncated || dropped || partialHead
	}
	sort.SliceStable(records, func(left, right int) bool {
		if !records[left].Timestamp.Equal(records[right].Timestamp) {
			return records[left].Timestamp.Before(records[right].Timestamp)
		}
		if records[left].DeploymentID != records[right].DeploymentID {
			return records[left].DeploymentID < records[right].DeploymentID
		}
		if records[left].AttemptID != records[right].AttemptID {
			return records[left].AttemptID < records[right].AttemptID
		}
		if records[left].segment != records[right].segment {
			return records[left].segment > records[right].segment
		}
		return records[left].offset < records[right].offset
	})
	return Window{Records: records, Truncated: truncated}, nil
}

func (reader *Reader) segments(query Query) ([]segment, error) {
	serviceRoot := filepath.Join(reader.root, "services", query.ServiceID)
	deployments, err := os.ReadDir(serviceRoot)
	if errors.Is(err, os.ErrNotExist) {
		return []segment{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list service log deployments: %w", err)
	}
	result := make([]segment, 0)
	for _, deployment := range deployments {
		if !deployment.IsDir() || !productID.MatchString(deployment.Name()) || (query.DeploymentID != "" && deployment.Name() != query.DeploymentID) {
			continue
		}
		files, readErr := os.ReadDir(filepath.Join(serviceRoot, deployment.Name()))
		if readErr != nil {
			return nil, fmt.Errorf("list deployment log attempts: %w", readErr)
		}
		for _, file := range files {
			match := segmentName.FindStringSubmatch(file.Name())
			if len(match) == 0 || file.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, infoErr := file.Info()
			if infoErr != nil {
				return nil, fmt.Errorf("inspect container log segment: %w", infoErr)
			}
			if !info.Mode().IsRegular() {
				continue
			}
			rotation := 0
			if match[2] != "" {
				rotation, _ = strconv.Atoi(match[2])
			}
			result = append(result, segment{
				path: filepath.Join(serviceRoot, deployment.Name(), file.Name()), deploymentID: deployment.Name(),
				attemptID: match[1], rotation: rotation, modifiedNano: info.ModTime().UnixNano(),
			})
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].modifiedNano != result[right].modifiedNano {
			return result[left].modifiedNano > result[right].modifiedNano
		}
		return result[left].path > result[right].path
	})
	return result, nil
}

func readSegmentTail(path string, budget int64) ([]byte, int64, int64, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, 0, false, err
	}
	size := info.Size()
	readBytes := min(size, budget)
	start := size - readBytes
	data := make([]byte, readBytes)
	read, err := file.ReadAt(data, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, 0, false, err
	}
	data = data[:read]
	return data, start, int64(read), start > 0, nil
}
