package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opencontainers/go-digest"
)

type ManagedPostgresExtension struct {
	PostgresID      string
	Name            string
	Version         string
	RecipeDigest    string
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

type PutManagedPostgresExtension struct {
	PostgresID      string
	Name            string
	Version         string
	RecipeDigest    string
	TimestampMillis int64
}

func (store *Store) PutManagedPostgresExtension(ctx context.Context, input PutManagedPostgresExtension) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Version = strings.TrimSpace(input.Version)
	if input.PostgresID == "" || !validExtensionName(input.Name) || input.Version == "" || input.TimestampMillis <= 0 {
		return errors.New("managed PostgreSQL extension input is invalid")
	}
	parsed, err := digest.Parse(input.RecipeDigest)
	if err != nil || parsed.Algorithm() != digest.SHA256 || parsed.Validate() != nil {
		return errors.New("managed PostgreSQL extension recipe digest is invalid")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM managed_postgres WHERE id = ?)", input.PostgresID,
		).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrManagedPostgresNotFound
		}
		_, err := transaction.ExecContext(ctx, `
INSERT INTO managed_postgres_extensions(
  postgres_id, name, version, recipe_digest, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(postgres_id, name) DO UPDATE SET
  version = excluded.version,
  recipe_digest = excluded.recipe_digest,
  updated_at = excluded.updated_at`,
			input.PostgresID, input.Name, input.Version, input.RecipeDigest,
			input.TimestampMillis, input.TimestampMillis,
		)
		return err
	})
}

func (store *Store) DeleteManagedPostgresExtension(ctx context.Context, postgresID, name string) error {
	name = strings.TrimSpace(name)
	if postgresID == "" || !validExtensionName(name) {
		return errors.New("managed PostgreSQL extension target is invalid")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx,
			"DELETE FROM managed_postgres_extensions WHERE postgres_id = ? AND name = ?",
			postgresID, name,
		)
		return err
	})
}

func (store *Store) ManagedPostgresExtensions(ctx context.Context, postgresID string) ([]ManagedPostgresExtension, error) {
	if postgresID == "" {
		return nil, ErrManagedPostgresNotFound
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT postgres_id, name, version, recipe_digest, created_at, updated_at
FROM managed_postgres_extensions
WHERE postgres_id = ?
ORDER BY name`, postgresID)
	if err != nil {
		return nil, fmt.Errorf("list managed PostgreSQL extensions: %w", err)
	}
	defer rows.Close()
	result := make([]ManagedPostgresExtension, 0)
	for rows.Next() {
		var extension ManagedPostgresExtension
		if err := rows.Scan(
			&extension.PostgresID, &extension.Name, &extension.Version,
			&extension.RecipeDigest, &extension.CreatedAtMillis, &extension.UpdatedAtMillis,
		); err != nil {
			return nil, fmt.Errorf("scan managed PostgreSQL extension: %w", err)
		}
		result = append(result, extension)
	}
	return result, rows.Err()
}

func (store *Store) AllManagedPostgresExtensions(ctx context.Context) ([]ManagedPostgresExtension, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT postgres_id, name, version, recipe_digest, created_at, updated_at
FROM managed_postgres_extensions
ORDER BY postgres_id, name`)
	if err != nil {
		return nil, fmt.Errorf("list all managed PostgreSQL extensions: %w", err)
	}
	defer rows.Close()
	result := make([]ManagedPostgresExtension, 0)
	for rows.Next() {
		var extension ManagedPostgresExtension
		if err := rows.Scan(
			&extension.PostgresID, &extension.Name, &extension.Version,
			&extension.RecipeDigest, &extension.CreatedAtMillis, &extension.UpdatedAtMillis,
		); err != nil {
			return nil, fmt.Errorf("scan managed PostgreSQL extension: %w", err)
		}
		result = append(result, extension)
	}
	return result, rows.Err()
}

func validExtensionName(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || character == '_' || (index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}
