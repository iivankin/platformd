package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/managedredis"
)

func managedRedisBrowserTool() Tool {
	return Tool{
		Name:        "scan_managed_redis_keys",
		Description: "Incrementally scan managed Redis keys with bounded read-only type, TTL, and memory metadata. Never uses KEYS.",
		InputSchema: objectSchema(map[string]any{
			"projectId": map[string]any{"type": "string"}, "redisId": map[string]any{"type": "string"},
			"cursor": map[string]any{"type": "string", "pattern": "^[0-9]+$"},
			"match":  map[string]any{"type": "string", "maxLength": 256},
			"count":  map[string]any{"type": "integer", "minimum": 1, "maximum": managedredis.MaximumScanCount},
		}, []string{"projectId", "redisId"}),
	}
}

func managedRedisPreviewTool() Tool {
	return Tool{
		Name:        "preview_managed_redis_key",
		Description: "Read a type-aware managed Redis value preview bounded to 100 elements and 64 KiB. The key is unpadded base64url.",
		InputSchema: objectSchema(map[string]any{
			"projectId": map[string]any{"type": "string"}, "redisId": map[string]any{"type": "string"},
			"key":   map[string]any{"type": "string", "description": "Unpadded base64url Redis key"},
			"count": map[string]any{"type": "integer", "minimum": 1, "maximum": managedredis.MaximumPreviewCount},
		}, []string{"projectId", "redisId", "key"}),
	}
}

func managedRedisAdminTool() Tool {
	return Tool{
		Name:        "create_managed_redis",
		Description: "Create a managed RDB-only Redis resource from an official image tag and return its generated password once. Requires an admin token.",
		InputSchema: objectSchema(map[string]any{
			"projectId":     map[string]any{"type": "string"},
			"name":          map[string]any{"type": "string"},
			"imageTag":      map[string]any{"type": "string"},
			"cpuMillicores": map[string]any{"type": "integer", "minimum": 0},
			"memoryBytes":   map[string]any{"type": "integer", "minimum": 0},
		}, []string{"projectId", "name", "imageTag"}),
	}
}

func (handler *Handler) createManagedRedis(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID     string `json:"projectId"`
		Name          string `json:"name"`
		ImageTag      string `json:"imageTag"`
		CPUMillicores int64  `json:"cpuMillicores"`
		MemoryBytes   int64  `json:"memoryBytes"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	result, err := handler.redis.Create(ctx, identity, automation.CreateManagedRedisInput{
		ProjectID: input.ProjectID, Name: input.Name, ImageTag: input.ImageTag,
		CPUMillicores: input.CPUMillicores, MemoryBytes: input.MemoryBytes,
	})
	if err != nil {
		return nil, err
	}
	resource := result.Resource
	return map[string]any{
		"redis": map[string]any{
			"id": resource.ID, "projectId": resource.ProjectID, "name": resource.Name,
			"hostname": resource.Name + "." + resource.ProjectName + ".internal", "port": 6379,
			"imageTag": resource.ImageTag, "imageDigest": resource.ImageDigest,
			"cpuMillicores": resource.CPUMillicores, "memoryBytes": resource.MemoryMaxBytes,
			"password": result.Password,
		},
		"requestId": result.RequestID,
	}, nil
}

func (handler *Handler) scanManagedRedisKeys(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		RedisID   string `json:"redisId"`
		Cursor    string `json:"cursor"`
		Match     string `json:"match"`
		Count     int    `json:"count"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" || input.RedisID == "" {
		return nil, errInvalidArguments
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	var cursor uint64
	if input.Cursor != "" {
		parsed, err := strconv.ParseUint(input.Cursor, 10, 64)
		if err != nil {
			return nil, errInvalidArguments
		}
		cursor = parsed
	}
	page, err := handler.redisBrowser.Keys(ctx, input.ProjectID, input.RedisID, managedredis.ScanQuery{
		Cursor: cursor, Match: input.Match, Count: input.Count,
	})
	if err != nil {
		return nil, err
	}
	keys := make([]map[string]any, 0, len(page.Keys))
	for _, key := range page.Keys {
		item := map[string]any{
			"keyBase64": base64.RawURLEncoding.EncodeToString(key.Key), "type": key.Type,
			"sizeBytes": key.SizeBytes,
		}
		if utf8.Valid(key.Key) {
			text := string(key.Key)
			if strings.IndexFunc(text, func(character rune) bool { return unicode.IsControl(character) }) < 0 {
				item["keyText"] = text
			}
		}
		if key.ExpiresInMillis != nil {
			item["expiresInMillis"] = *key.ExpiresInMillis
		}
		keys = append(keys, item)
	}
	return map[string]any{"nextCursor": strconv.FormatUint(page.NextCursor, 10), "keys": keys}, nil
}

func (handler *Handler) previewManagedRedisKey(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string  `json:"projectId"`
		RedisID   string  `json:"redisId"`
		Key       *string `json:"key"`
		Count     int     `json:"count"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" || input.RedisID == "" || input.Key == nil {
		return nil, errInvalidArguments
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	key, err := base64.RawURLEncoding.DecodeString(*input.Key)
	if err != nil {
		return nil, errInvalidArguments
	}
	preview, err := handler.redisBrowser.Preview(ctx, input.ProjectID, input.RedisID, managedredis.PreviewQuery{Key: key, Count: input.Count})
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(preview.Items))
	for _, item := range preview.Items {
		values := make([]map[string]string, 0, len(item.Values))
		for _, value := range item.Values {
			encoded := map[string]string{"base64": base64.RawURLEncoding.EncodeToString(value)}
			if utf8.Valid(value) {
				text := string(value)
				if strings.IndexFunc(text, func(character rune) bool { return unicode.IsControl(character) }) < 0 {
					encoded["text"] = text
				}
			}
			values = append(values, encoded)
		}
		items = append(items, map[string]any{"values": values})
	}
	return map[string]any{
		"type": preview.Type, "length": preview.Length,
		"nextCursor": strconv.FormatUint(preview.NextCursor, 10),
		"truncated":  preview.Truncated, "items": items,
	}, nil
}
