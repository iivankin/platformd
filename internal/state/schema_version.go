package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
)

type DatabaseInspection struct {
	SchemaVersion int
}

func SupportedSchemaVersion() int {
	return currentSchemaVersion
}

func ReadSchemaVersion(ctx context.Context, path string, expectedUID int) (int, error) {
	inspection, err := InspectDatabase(ctx, path, expectedUID, false)
	return inspection.SchemaVersion, err
}

// InspectDatabase opens an existing image read-only. It deliberately does not
// call Open: bootstrap restore must validate the saved schema without migrating
// it under a different platformd release.
func InspectDatabase(ctx context.Context, path string, expectedUID int, checkIntegrity bool) (DatabaseInspection, error) {
	if _, err := os.Lstat(path); err != nil {
		return DatabaseInspection{}, fmt.Errorf("inspect SQLite before read-only schema check: %w", err)
	}
	if err := prepareDatabaseFile(path, expectedUID); err != nil {
		return DatabaseInspection{}, err
	}
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("_query_only", "true")
	query.Set("cache", "private")
	query.Set("mode", "ro")
	uri.RawQuery = query.Encode()
	database, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		return DatabaseInspection{}, err
	}
	var version int
	readErr := database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	if readErr == nil && checkIntegrity {
		var integrity string
		readErr = database.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity)
		if readErr == nil && integrity != "ok" {
			readErr = fmt.Errorf("SQLite integrity_check = %q", integrity)
		}
	}
	if readErr == nil && checkIntegrity {
		rows, err := database.QueryContext(ctx, "PRAGMA foreign_key_check")
		if err != nil {
			readErr = err
		} else if rows.Next() {
			readErr = errors.New("SQLite foreign_key_check found a violation")
		}
		if rows != nil {
			readErr = errors.Join(readErr, rows.Err(), rows.Close())
		}
	}
	closeErr := database.Close()
	if readErr != nil || closeErr != nil {
		return DatabaseInspection{}, errors.Join(readErr, closeErr)
	}
	return DatabaseInspection{SchemaVersion: version}, nil
}
