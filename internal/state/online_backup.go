package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/mattn/go-sqlite3"
)

const backupStepPages = 256

func (store *Store) OnlineBackup(ctx context.Context, destination string) error {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || destination == string(filepath.Separator) {
		return errors.New("SQLite backup destination must be a canonical absolute path")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("SQLite backup destination already exists")
		}
		return err
	}
	uri := url.URL{Scheme: "file", Path: destination}
	query := uri.Query()
	query.Set("_journal_mode", "DELETE")
	query.Set("_synchronous", "FULL")
	query.Set("mode", "rwc")
	uri.RawQuery = query.Encode()
	destinationDB, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		return err
	}
	destinationDB.SetMaxOpenConns(1)
	cleanup := func(result error) error {
		closeErr := destinationDB.Close()
		if result != nil {
			_ = os.Remove(destination)
		}
		return errors.Join(result, closeErr)
	}
	sourceConnection, err := store.database.Conn(ctx)
	if err != nil {
		return cleanup(err)
	}
	defer sourceConnection.Close()
	destinationConnection, err := destinationDB.Conn(ctx)
	if err != nil {
		return cleanup(err)
	}

	err = destinationConnection.Raw(func(destinationDriver any) error {
		destinationSQLite, ok := destinationDriver.(*sqlite3.SQLiteConn)
		if !ok {
			return errors.New("SQLite backup destination driver is unsupported")
		}
		return sourceConnection.Raw(func(sourceDriver any) error {
			sourceSQLite, ok := sourceDriver.(*sqlite3.SQLiteConn)
			if !ok {
				return errors.New("SQLite backup source driver is unsupported")
			}
			backup, err := destinationSQLite.Backup("main", sourceSQLite, "main")
			if err != nil {
				return err
			}
			for {
				done, stepErr := backup.Step(backupStepPages)
				if stepErr != nil {
					return errors.Join(stepErr, backup.Close())
				}
				if done {
					return backup.Close()
				}
				select {
				case <-ctx.Done():
					return errors.Join(ctx.Err(), backup.Close())
				case <-time.After(time.Millisecond):
				}
			}
		})
	})
	if err != nil {
		_ = destinationConnection.Close()
		return cleanup(fmt.Errorf("copy SQLite online backup: %w", err))
	}
	if err := destinationConnection.Close(); err != nil {
		return cleanup(err)
	}
	if err := destinationDB.Close(); err != nil {
		_ = os.Remove(destination)
		return err
	}
	info, err := os.Lstat(destination)
	if err != nil || !info.Mode().IsRegular() {
		_ = os.Remove(destination)
		return errors.New("SQLite online backup is not a regular file")
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		_ = os.Remove(destination)
		return err
	}
	file, err := os.Open(destination)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(syncErr, closeErr, syncDirectory(filepath.Dir(destination)))
}
