package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/iivankin/platformd/internal/databaseversion"
	"github.com/iivankin/platformd/internal/diskpressure"
	"github.com/iivankin/platformd/internal/fsusage"
	"github.com/iivankin/platformd/internal/managedpostgres"
)

func (stack *runtimeStack) postgresVersionCapacity(
	ctx context.Context,
	resource databaseversion.Resource,
) (databaseversion.Capacity, error) {
	result, err := stack.QueryManagedPostgres(ctx, resource.ID, "SELECT pg_database_size(current_database()) AS bytes")
	if err != nil {
		return databaseversion.Capacity{}, fmt.Errorf("read managed PostgreSQL database size: %w", err)
	}
	dataBytes, err := postgresSizeResult(result)
	if err != nil {
		return databaseversion.Capacity{}, err
	}
	return stack.databaseVolumeCapacity(resource, dataBytes)
}

func (stack *runtimeStack) redisVersionCapacity(
	_ context.Context,
	resource databaseversion.Resource,
) (databaseversion.Capacity, error) {
	volume, err := stack.databaseVolumePath(resource)
	if err != nil {
		return databaseversion.Capacity{}, err
	}
	rdb := filepath.Join(volume, "dump.rdb")
	info, err := os.Lstat(rdb)
	if errors.Is(err, os.ErrNotExist) {
		return stack.databaseVolumeCapacity(resource, 0)
	}
	if err != nil {
		return databaseversion.Capacity{}, fmt.Errorf("inspect managed Redis RDB: %w", err)
	}
	if !info.Mode().IsRegular() {
		return databaseversion.Capacity{}, errors.New("managed Redis RDB is not a regular file")
	}
	return stack.databaseVolumeCapacity(resource, uint64(info.Size()))
}

func (stack *runtimeStack) databaseVolumeCapacity(
	resource databaseversion.Resource,
	dataBytes uint64,
) (databaseversion.Capacity, error) {
	volume, err := stack.databaseVolumePath(resource)
	if err != nil {
		return databaseversion.Capacity{}, err
	}
	volumeBytes, err := fsusage.DirectoryBytes(volume)
	if err != nil {
		return databaseversion.Capacity{}, err
	}
	usage, err := (diskpressure.StatfsCollector{}).Collect(stack.paths.VolumesRoot)
	if err != nil {
		return databaseversion.Capacity{}, fmt.Errorf("read managed database filesystem capacity: %w", err)
	}
	required := max(dataBytes, volumeBytes)
	return databaseversion.Capacity{
		CurrentDataBytes: dataBytes, RequiredFreeBytes: required, AvailableBytes: usage.AvailableBytes,
	}, nil
}

func (stack *runtimeStack) databaseVolumePath(resource databaseversion.Resource) (string, error) {
	if !safeDatabaseVolumeComponent(resource.ProjectID) || !safeDatabaseVolumeComponent(resource.VolumeID) {
		return "", errors.New("managed database volume identity is invalid")
	}
	return filepath.Join(stack.paths.VolumesRoot, resource.ProjectID, resource.VolumeID), nil
}

func postgresSizeResult(result managedpostgres.QueryResult) (uint64, error) {
	if result.Truncated || len(result.Statements) != 1 || result.Statements[0].Truncated ||
		len(result.Statements[0].Rows) != 1 || len(result.Statements[0].Rows[0]) != 1 {
		return 0, errors.New("managed PostgreSQL database size query returned an unexpected shape")
	}
	cell := result.Statements[0].Rows[0][0]
	if cell.Null || cell.Base64 != "" {
		return 0, errors.New("managed PostgreSQL database size query returned a non-text value")
	}
	value, err := strconv.ParseUint(cell.Text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse managed PostgreSQL database size: %w", err)
	}
	return value, nil
}

func safeDatabaseVolumeComponent(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value &&
		!strings.ContainsAny(value, "/\\\x00")
}
