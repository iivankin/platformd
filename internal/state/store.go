package state

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	_ "github.com/mattn/go-sqlite3"
)

const (
	currentSchemaVersion = 6
	writerQueueSize      = 128
)

var (
	ErrClosed = errors.New("state store is closed")

	//go:embed schema.sql
	initialSchema string

	//go:embed migration_2.sql
	migration2 string

	//go:embed migration_3.sql
	migration3 string

	//go:embed migration_4.sql
	migration4 string

	//go:embed migration_5.sql
	migration5 string

	//go:embed migration_6.sql
	migration6 string
)

type Store struct {
	database        *sql.DB
	writer          *writer
	close           sync.Once
	controlObserver sync.RWMutex
	onControlCommit func()
}

func Open(ctx context.Context, path string, expectedUID int) (*Store, error) {
	if err := prepareDatabaseFile(path, expectedUID); err != nil {
		return nil, err
	}

	database, err := sql.Open("sqlite3", dataSourceName(path))
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	database.SetMaxIdleConns(4)
	database.SetMaxOpenConns(8)

	cleanup := func() {
		_ = database.Close()
	}
	if err := database.PingContext(ctx); err != nil {
		cleanup()
		return nil, fmt.Errorf("ping SQLite: %w", err)
	}
	if err := validateSQLite(ctx, database); err != nil {
		cleanup()
		return nil, err
	}
	if err := migrate(ctx, database); err != nil {
		cleanup()
		return nil, err
	}

	return &Store{database: database, writer: newWriter(database)}, nil
}

func (store *Store) Close() error {
	var err error
	store.close.Do(func() {
		store.writer.Close()
		err = store.database.Close()
	})
	return err
}

func (store *Store) Write(ctx context.Context, action func(*sql.Tx) error) error {
	return store.writer.Transaction(ctx, action)
}

func (store *Store) WriteControl(ctx context.Context, action func(*sql.Tx) error) error {
	if err := store.Write(ctx, action); err != nil {
		return err
	}
	store.controlObserver.RLock()
	observer := store.onControlCommit
	store.controlObserver.RUnlock()
	if observer != nil {
		observer()
	}
	return nil
}

func (store *Store) SetControlCommitObserver(observer func()) {
	store.controlObserver.Lock()
	store.onControlCommit = observer
	store.controlObserver.Unlock()
}

func (store *Store) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return store.database.QueryRowContext(ctx, query, args...)
}

func (store *Store) Checkpoint(ctx context.Context) error {
	var busy, logFrames, checkpointed int
	if err := store.database.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("checkpoint SQLite: %w", err)
	}
	if busy != 0 || logFrames != checkpointed {
		return fmt.Errorf("SQLite checkpoint incomplete: busy=%d log=%d checkpointed=%d", busy, logFrames, checkpointed)
	}
	return nil
}

func (store *Store) MarkInterrupted(ctx context.Context, timestampMillis int64) error {
	return store.Write(ctx, func(transaction *sql.Tx) error {
		statements := []string{
			"UPDATE operations SET status = 'interrupted', finished_at = ? WHERE status = 'running'",
			"UPDATE backups SET status = 'interrupted', finished_at = ? WHERE status = 'running'",
			"UPDATE deployments SET status = 'interrupted', finished_at = ? WHERE status = 'running' AND id NOT IN (SELECT active_deployment_id FROM services WHERE active_deployment_id IS NOT NULL)",
		}
		for _, statement := range statements {
			if _, err := transaction.ExecContext(ctx, statement, timestampMillis); err != nil {
				return fmt.Errorf("mark interrupted state: %w", err)
			}
		}
		return nil
	})
}

func dataSourceName(path string) string {
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("_busy_timeout", "5000")
	query.Set("_foreign_keys", "on")
	query.Set("_journal_mode", "WAL")
	query.Set("_secure_delete", "FAST")
	query.Set("_synchronous", "FULL")
	query.Set("_txlock", "immediate")
	query.Set("cache", "private")
	query.Set("mode", "rw")
	uri.RawQuery = query.Encode()
	return uri.String()
}

func prepareDatabaseFile(path string, expectedUID int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		file, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil {
			return fmt.Errorf("create SQLite file: %w", createErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("close new SQLite file: %w", closeErr)
		}
		if syncErr := syncDirectory(filepath.Dir(path)); syncErr != nil {
			return syncErr
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect SQLite file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("SQLite path is not a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("SQLite mode = %04o, want 0600", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("SQLite ownership is unavailable")
	}
	if int(stat.Uid) != expectedUID {
		return fmt.Errorf("SQLite uid = %d, want %d", stat.Uid, expectedUID)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open state directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync state directory: %w", err)
	}
	return nil
}

func validateSQLite(ctx context.Context, database *sql.DB) error {
	var version string
	if err := database.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return fmt.Errorf("read SQLite version: %w", err)
	}
	if compareSQLiteVersion(version, [3]int{3, 53, 2}) != 0 {
		return fmt.Errorf("SQLite version = %s, want exact 3.53.2", version)
	}

	var journalMode string
	if err := database.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return fmt.Errorf("read SQLite journal mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("SQLite journal mode = %s, want wal", journalMode)
	}
	var foreignKeys int
	if err := database.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("read SQLite foreign key mode: %w", err)
	}
	if foreignKeys != 1 {
		return errors.New("SQLite foreign keys are disabled")
	}
	return nil
}

func compareSQLiteVersion(value string, minimum [3]int) int {
	parts := strings.Split(value, ".")
	for index := range minimum {
		if index >= len(parts) {
			return -1
		}
		part, err := strconv.Atoi(parts[index])
		if err != nil {
			return -1
		}
		if part < minimum[index] {
			return -1
		}
		if part > minimum[index] {
			return 1
		}
	}
	return 0
}

func migrate(ctx context.Context, database *sql.DB) error {
	var version int
	if err := database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	switch version {
	case currentSchemaVersion:
		return nil
	case 0:
		transaction, err := database.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin initial migration: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, initialSchema); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("apply initial migration: %w", err)
		}
		if err := transaction.Commit(); err != nil {
			return fmt.Errorf("commit initial migration: %w", err)
		}
		return nil
	case 1:
		if err := applyMigration(ctx, database, migration2, 2); err != nil {
			return err
		}
		if err := applyMigration(ctx, database, migration3, 3); err != nil {
			return err
		}
		if err := applyMigration(ctx, database, migration4, 4); err != nil {
			return err
		}
		if err := applyMigration(ctx, database, migration5, 5); err != nil {
			return err
		}
		return applyMigration(ctx, database, migration6, 6)
	case 2:
		if err := applyMigration(ctx, database, migration3, 3); err != nil {
			return err
		}
		if err := applyMigration(ctx, database, migration4, 4); err != nil {
			return err
		}
		if err := applyMigration(ctx, database, migration5, 5); err != nil {
			return err
		}
		return applyMigration(ctx, database, migration6, 6)
	case 3:
		if err := applyMigration(ctx, database, migration4, 4); err != nil {
			return err
		}
		if err := applyMigration(ctx, database, migration5, 5); err != nil {
			return err
		}
		return applyMigration(ctx, database, migration6, 6)
	case 4:
		if err := applyMigration(ctx, database, migration5, 5); err != nil {
			return err
		}
		return applyMigration(ctx, database, migration6, 6)
	case 5:
		return applyMigration(ctx, database, migration6, 6)
	default:
		return fmt.Errorf("unsupported SQLite schema version %d; this binary supports exactly %d", version, currentSchemaVersion)
	}
}

func applyMigration(ctx context.Context, database *sql.DB, statements string, version int) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema migration %d: %w", version, err)
	}
	if _, err := transaction.ExecContext(ctx, statements); err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("apply schema migration %d: %w", version, err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit schema migration %d: %w", version, err)
	}
	return nil
}
