package state

import (
	"context"
	"fmt"
)

type ControlResourceIDs struct {
	RegistryRepositories []string `json:"registryRepositories"`
	ObjectStores         []string `json:"objectStores"`
	Postgres             []string `json:"postgres"`
	Redis                []string `json:"redis"`
	Volumes              []string `json:"volumes"`
}

func (store *Store) ControlResources(ctx context.Context) (ControlResourceIDs, error) {
	result := ControlResourceIDs{}
	queries := []struct {
		target *[]string
		query  string
	}{
		{&result.RegistryRepositories, "SELECT id FROM registry_repositories ORDER BY id"},
		{&result.ObjectStores, "SELECT id FROM object_stores ORDER BY id"},
		{&result.Postgres, "SELECT id FROM managed_postgres ORDER BY id"},
		{&result.Redis, "SELECT id FROM managed_redis ORDER BY id"},
		{&result.Volumes, "SELECT id FROM volumes ORDER BY id"},
	}
	for _, item := range queries {
		rows, err := store.database.QueryContext(ctx, item.query)
		if err != nil {
			return ControlResourceIDs{}, err
		}
		for rows.Next() {
			var identifier string
			if err := rows.Scan(&identifier); err != nil {
				_ = rows.Close()
				return ControlResourceIDs{}, err
			}
			*item.target = append(*item.target, identifier)
		}
		if err := rows.Close(); err != nil {
			return ControlResourceIDs{}, fmt.Errorf("close control resource query: %w", err)
		}
	}
	return result, nil
}
