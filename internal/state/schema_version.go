package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
)

func SupportedSchemaVersion() int {
	return currentSchemaVersion
}

func ReadSchemaVersion(ctx context.Context, path string, expectedUID int) (int, error) {
	if _, err := os.Lstat(path); err != nil {
		return 0, fmt.Errorf("inspect SQLite before read-only schema check: %w", err)
	}
	if err := prepareDatabaseFile(path, expectedUID); err != nil {
		return 0, err
	}
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("_query_only", "true")
	query.Set("cache", "private")
	query.Set("mode", "ro")
	uri.RawQuery = query.Encode()
	database, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		return 0, err
	}
	var version int
	readErr := database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	closeErr := database.Close()
	if readErr != nil || closeErr != nil {
		return 0, errors.Join(readErr, closeErr)
	}
	return version, nil
}
