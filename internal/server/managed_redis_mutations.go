package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/managedredis"
)

const maximumRedisMutationRequestBytes = 384 << 10

type encodedFieldValue struct {
	Field *string `json:"field"`
	Value *string `json:"value"`
}

type redisMutationRequest struct {
	Operation managedredis.MutationKind `json:"operation"`
	Key       *string                   `json:"key"`
	Field     *string                   `json:"field"`
	Value     *string                   `json:"value"`
	Member    *string                   `json:"member"`
	Score     *float64                  `json:"score"`
	Index     *int64                    `json:"index"`
	Count     *int64                    `json:"count"`
	StreamID  *string                   `json:"streamId"`
	Fields    []encodedFieldValue       `json:"fields"`
	TTLMillis *int64                    `json:"ttlMillis"`
}

func mutateManagedRedisData(repository ManagedRedisRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumRedisMutationRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body redisMutationRequest
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body must contain only type-aware Redis mutation fields")
			return
		}
		mutation, err := body.mutation()
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_redis_mutation", err.Error())
			return
		}
		result, err := repository.Mutate(request.Context(), managedredis.DataMutationInput{
			ProjectID: request.PathValue("projectID"), ResourceID: request.PathValue("redisID"),
			Actor:    managedredis.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
			Mutation: mutation,
		})
		if err != nil {
			writeManagedRedisError(response, err)
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusOK, map[string]any{
			"affected": result.Affected, "streamId": base64.RawURLEncoding.EncodeToString(result.StreamID),
			"auditRecorded": result.AuditRecorded,
		})
	}
}

func (request redisMutationRequest) mutation() (managedredis.Mutation, error) {
	if request.Key == nil {
		return managedredis.Mutation{}, errors.New("key is required as unpadded base64url")
	}
	key, err := decodeRedisBytes(*request.Key, "key")
	if err != nil {
		return managedredis.Mutation{}, err
	}
	mutation := managedredis.Mutation{Kind: request.Operation, Key: key, Score: request.Score, Index: request.Index}
	require := func(value *string, name string) ([]byte, error) {
		if value == nil {
			return nil, errors.New(name + " is required as unpadded base64url")
		}
		return decodeRedisBytes(*value, name)
	}
	switch request.Operation {
	case managedredis.MutationStringSet, managedredis.MutationListPushLeft, managedredis.MutationListPushRight, managedredis.MutationListSet, managedredis.MutationListRemove:
		mutation.Value, err = require(request.Value, "value")
		if request.Operation == managedredis.MutationListSet && request.Index == nil {
			return managedredis.Mutation{}, errors.New("index is required for list_set")
		}
		if request.Operation == managedredis.MutationListRemove {
			mutation.Count = 1
			if request.Count != nil {
				mutation.Count = *request.Count
			}
		}
	case managedredis.MutationHashSet:
		mutation.Field, err = require(request.Field, "field")
		if err == nil {
			mutation.Value, err = require(request.Value, "value")
		}
	case managedredis.MutationHashDelete:
		mutation.Field, err = require(request.Field, "field")
	case managedredis.MutationSetAdd, managedredis.MutationSetRemove, managedredis.MutationZSetAdd, managedredis.MutationZSetRemove:
		mutation.Member, err = require(request.Member, "member")
		if request.Operation == managedredis.MutationZSetAdd && request.Score == nil {
			return managedredis.Mutation{}, errors.New("score is required for zset_add")
		}
	case managedredis.MutationStreamAdd:
		mutation.Fields = make([]managedredis.FieldValue, 0, len(request.Fields))
		for _, pair := range request.Fields {
			field, fieldErr := require(pair.Field, "stream field")
			if fieldErr != nil {
				return managedredis.Mutation{}, fieldErr
			}
			value, valueErr := require(pair.Value, "stream value")
			if valueErr != nil {
				return managedredis.Mutation{}, valueErr
			}
			mutation.Fields = append(mutation.Fields, managedredis.FieldValue{Field: field, Value: value})
		}
	case managedredis.MutationStreamDelete:
		mutation.StreamID, err = require(request.StreamID, "streamId")
	case managedredis.MutationTTLSet:
		if request.TTLMillis == nil {
			return managedredis.Mutation{}, errors.New("ttlMillis is required for ttl_set")
		}
		mutation.TTLMillis = *request.TTLMillis
	case managedredis.MutationTTLClear, managedredis.MutationKeyDelete:
	default:
		return managedredis.Mutation{}, errors.New("unsupported type-aware Redis operation")
	}
	if err != nil {
		return managedredis.Mutation{}, err
	}
	return mutation, nil
}

func decodeRedisBytes(value, name string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.New(name + " must be unpadded base64url")
	}
	return decoded, nil
}
