package mcp

import (
	"context"
	"encoding/json"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/managedredis"
)

func backupAdminTools() []Tool {
	kind := map[string]any{"type": "string", "enum": []string{"service", "postgres", "redis", "object_store"}}
	return []Tool{
		{
			Name: "set_backup_policy", Description: "Set the backup policy for one managed resource. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"}, "kind": kind, "resourceId": map[string]any{"type": "string"},
				"targetId": map[string]any{"type": "string"}, "enabled": map[string]any{"type": "boolean"},
				"cron": map[string]any{"type": "string"}, "retentionCount": map[string]any{"type": "integer", "minimum": 1},
			}, []string{"projectId", "kind", "resourceId", "enabled"}),
		},
		{
			Name: "run_backup", Description: "Run a backup now for one managed resource. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"}, "kind": kind, "resourceId": map[string]any{"type": "string"},
				"targetId": map[string]any{"type": "string"},
			}, []string{"projectId", "kind", "resourceId"}),
		},
		{
			Name: "restore_backup", Description: "Restore one managed resource backup generation. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"}, "kind": kind, "resourceId": map[string]any{"type": "string"},
				"targetId": map[string]any{"type": "string"}, "generationId": map[string]any{"type": "string"},
				"mode": map[string]any{"type": "string"}, "newResourceName": map[string]any{"type": "string"},
				"destructiveConfirmed": map[string]any{"type": "boolean"},
			}, []string{"projectId", "kind", "resourceId", "generationId"}),
		},
	}
}

func queryManagedPostgresTool() Tool {
	return Tool{
		Name: "query_managed_postgres", Description: "Run a bounded SQL query against managed PostgreSQL. Requires an admin token.",
		InputSchema: objectSchema(map[string]any{
			"projectId": map[string]any{"type": "string"}, "resourceId": map[string]any{"type": "string"},
			"sql": map[string]any{"type": "string"},
		}, []string{"projectId", "resourceId", "sql"}),
	}
}

func mutateRedisKeyTool() Tool {
	return Tool{
		Name: "mutate_redis_key", Description: "Apply one typed Redis data mutation. Requires an admin token.",
		InputSchema: objectSchema(map[string]any{
			"projectId": map[string]any{"type": "string"}, "resourceId": map[string]any{"type": "string"},
			"operation": map[string]any{"type": "string"},
			"key":       map[string]any{"type": "string"},
			"field":     map[string]any{"type": "string"},
			"value":     map[string]any{"type": "string"},
			"member":    map[string]any{"type": "string"},
			"score":     map[string]any{"type": "number"},
			"index":     map[string]any{"type": "integer"},
			"count":     map[string]any{"type": "integer"},
			"streamId":  map[string]any{"type": "string"},
			"ttlMillis": map[string]any{"type": "integer"},
		}, []string{"projectId", "resourceId", "operation", "key"}),
	}
}

func redisReadTools() []Tool {
	return []Tool{
		{
			Name: "scan_redis_keys", Description: "Scan keys in one managed Redis resource.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"}, "resourceId": map[string]any{"type": "string"},
				"cursor": map[string]any{"type": "integer", "minimum": 0},
				"match":  map[string]any{"type": "string"},
				"count":  map[string]any{"type": "integer", "minimum": 1, "maximum": managedredis.MaximumScanCount},
			}, []string{"projectId", "resourceId"}),
		},
		{
			Name: "preview_redis_key", Description: "Preview one managed Redis key value.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"}, "resourceId": map[string]any{"type": "string"},
				"key": map[string]any{"type": "string"}, "count": map[string]any{"type": "integer", "minimum": 1, "maximum": managedredis.MaximumPreviewCount},
			}, []string{"projectId", "resourceId", "key"}),
		},
	}
}

func (handler *Handler) setBackupPolicy(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID      string `json:"projectId"`
		Kind           string `json:"kind"`
		ResourceID     string `json:"resourceId"`
		TargetID       string `json:"targetId"`
		Enabled        bool   `json:"enabled"`
		Cron           string `json:"cron"`
		RetentionCount int    `json:"retentionCount"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	result, err := handler.backups.SetPolicy(ctx, identity, automation.SetBackupPolicyInput{
		ProjectID: input.ProjectID, Kind: input.Kind, ResourceID: input.ResourceID, TargetID: input.TargetID,
		Enabled: input.Enabled, Cron: input.Cron, RetentionCount: input.RetentionCount,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"policy": result.Policy, "nextRunAt": result.NextRunAtMillis, "requestId": result.RequestID}, nil
}

func (handler *Handler) runBackup(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID  string `json:"projectId"`
		Kind       string `json:"kind"`
		ResourceID string `json:"resourceId"`
		TargetID   string `json:"targetId"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	record, err := handler.backups.RunNow(ctx, identity, automation.RunBackupInput{
		ProjectID: input.ProjectID, Kind: input.Kind, ResourceID: input.ResourceID, TargetID: input.TargetID,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"backup": record}, nil
}

func (handler *Handler) restoreBackup(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID            string `json:"projectId"`
		Kind                 string `json:"kind"`
		ResourceID           string `json:"resourceId"`
		TargetID             string `json:"targetId"`
		GenerationID         string `json:"generationId"`
		Mode                 string `json:"mode"`
		NewResourceName      string `json:"newResourceName"`
		DestructiveConfirmed bool   `json:"destructiveConfirmed"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	operation, err := handler.backups.Restore(ctx, identity, automation.RestoreBackupInput{
		ProjectID: input.ProjectID, Kind: input.Kind, ResourceID: input.ResourceID, TargetID: input.TargetID,
		GenerationID: input.GenerationID, Mode: input.Mode, NewResourceName: input.NewResourceName,
		DestructiveConfirmed: input.DestructiveConfirmed,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"operation": operation}, nil
}

func (handler *Handler) queryManagedPostgres(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID  string `json:"projectId"`
		ResourceID string `json:"resourceId"`
		SQL        string `json:"sql"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	return handler.postgres.Query(ctx, identity, automation.QueryManagedPostgresInput{
		ProjectID: input.ProjectID, ResourceID: input.ResourceID, SQL: input.SQL,
	})
}

func (handler *Handler) scanRedisKeys(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID  string `json:"projectId"`
		ResourceID string `json:"resourceId"`
		Cursor     uint64 `json:"cursor"`
		Match      string `json:"match"`
		Count      int    `json:"count"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	page, err := handler.redis.Keys(ctx, identity, automation.ScanRedisKeysInput{
		ProjectID: input.ProjectID, ResourceID: input.ResourceID, Cursor: input.Cursor, Match: input.Match, Count: input.Count,
	})
	if err != nil {
		return nil, err
	}
	keys := make([]map[string]any, 0, len(page.Keys))
	for _, key := range page.Keys {
		item := map[string]any{"key": string(key.Key), "type": key.Type, "sizeBytes": key.SizeBytes}
		if key.ExpiresInMillis != nil {
			item["expiresInMillis"] = *key.ExpiresInMillis
		}
		keys = append(keys, item)
	}
	return map[string]any{"nextCursor": page.NextCursor, "keys": keys}, nil
}

func (handler *Handler) previewRedisKey(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID  string `json:"projectId"`
		ResourceID string `json:"resourceId"`
		Key        string `json:"key"`
		Count      int    `json:"count"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	return handler.redis.Preview(ctx, identity, automation.PreviewRedisKeyInput{
		ProjectID: input.ProjectID, ResourceID: input.ResourceID, Key: input.Key, Count: input.Count,
	})
}

func (handler *Handler) mutateRedisKey(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID  string   `json:"projectId"`
		ResourceID string   `json:"resourceId"`
		Operation  string   `json:"operation"`
		Key        string   `json:"key"`
		Field      string   `json:"field"`
		Value      string   `json:"value"`
		Member     string   `json:"member"`
		Score      *float64 `json:"score"`
		Index      *int64   `json:"index"`
		Count      int64    `json:"count"`
		StreamID   string   `json:"streamId"`
		TTLMillis  int64    `json:"ttlMillis"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	return handler.redis.Mutate(ctx, identity, automation.MutateRedisKeyInput{
		ProjectID: input.ProjectID, ResourceID: input.ResourceID,
		Mutation: managedredis.Mutation{
			Kind: managedredis.MutationKind(input.Operation), Key: []byte(input.Key),
			Field: []byte(input.Field), Value: []byte(input.Value), Member: []byte(input.Member),
			Score: input.Score, Index: input.Index, Count: input.Count,
			StreamID: []byte(input.StreamID), TTLMillis: input.TTLMillis,
		},
	})
}
