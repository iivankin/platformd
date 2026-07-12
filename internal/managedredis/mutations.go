package managedredis

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
)

const (
	MaximumMutationValueBytes = 64 << 10
	maximumMutationBytes      = 256 << 10
	maximumStreamFields       = 100
)

type MutationKind string

const (
	MutationStringSet     MutationKind = "string_set"
	MutationHashSet       MutationKind = "hash_set"
	MutationHashDelete    MutationKind = "hash_delete"
	MutationListPushLeft  MutationKind = "list_push_left"
	MutationListPushRight MutationKind = "list_push_right"
	MutationListSet       MutationKind = "list_set"
	MutationListRemove    MutationKind = "list_remove"
	MutationSetAdd        MutationKind = "set_add"
	MutationSetRemove     MutationKind = "set_remove"
	MutationZSetAdd       MutationKind = "zset_add"
	MutationZSetRemove    MutationKind = "zset_remove"
	MutationStreamAdd     MutationKind = "stream_add"
	MutationStreamDelete  MutationKind = "stream_delete"
	MutationKeyDelete     MutationKind = "key_delete"
	MutationTTLSet        MutationKind = "ttl_set"
	MutationTTLClear      MutationKind = "ttl_clear"
)

type FieldValue struct {
	Field []byte
	Value []byte
}

type Mutation struct {
	Kind      MutationKind
	Key       []byte
	Field     []byte
	Value     []byte
	Member    []byte
	Score     *float64
	Index     *int64
	Count     int64
	StreamID  []byte
	Fields    []FieldValue
	TTLMillis int64
}

type MutationResult struct {
	Affected int64
	StreamID []byte
}

func (client *Client) Mutate(ctx context.Context, mutation Mutation) (MutationResult, error) {
	if err := validateMutation(mutation); err != nil {
		return MutationResult{}, err
	}
	key := string(mutation.Key)
	switch mutation.Kind {
	case MutationStringSet:
		return client.expectMutationOK(ctx, "SET", key, string(mutation.Value))
	case MutationHashSet:
		return client.integerMutation(ctx, "HSET", key, string(mutation.Field), string(mutation.Value))
	case MutationHashDelete:
		return client.integerMutation(ctx, "HDEL", key, string(mutation.Field))
	case MutationListPushLeft:
		return client.integerMutation(ctx, "LPUSH", key, string(mutation.Value))
	case MutationListPushRight:
		return client.integerMutation(ctx, "RPUSH", key, string(mutation.Value))
	case MutationListSet:
		return client.expectMutationOK(ctx, "LSET", key, strconv.FormatInt(*mutation.Index, 10), string(mutation.Value))
	case MutationListRemove:
		return client.integerMutation(ctx, "LREM", key, strconv.FormatInt(mutation.Count, 10), string(mutation.Value))
	case MutationSetAdd:
		return client.integerMutation(ctx, "SADD", key, string(mutation.Member))
	case MutationSetRemove:
		return client.integerMutation(ctx, "SREM", key, string(mutation.Member))
	case MutationZSetAdd:
		return client.integerMutation(ctx, "ZADD", key, strconv.FormatFloat(*mutation.Score, 'g', -1, 64), string(mutation.Member))
	case MutationZSetRemove:
		return client.integerMutation(ctx, "ZREM", key, string(mutation.Member))
	case MutationStreamAdd:
		arguments := []string{"XADD", key, "*"}
		for _, field := range mutation.Fields {
			arguments = append(arguments, string(field.Field), string(field.Value))
		}
		value, err := client.command(ctx, arguments...)
		if err != nil {
			return MutationResult{}, err
		}
		if value.kind != responseBulk {
			return MutationResult{}, errors.New("Redis XADD response has an unexpected shape")
		}
		return MutationResult{Affected: 1, StreamID: append([]byte(nil), value.bulk...)}, nil
	case MutationStreamDelete:
		return client.integerMutation(ctx, "XDEL", key, string(mutation.StreamID))
	case MutationKeyDelete:
		return client.integerMutation(ctx, "DEL", key)
	case MutationTTLSet:
		return client.integerMutation(ctx, "PEXPIRE", key, strconv.FormatInt(mutation.TTLMillis, 10))
	case MutationTTLClear:
		return client.integerMutation(ctx, "PERSIST", key)
	default:
		panic("validated managed Redis mutation kind")
	}
}

func validateMutation(mutation Mutation) error {
	bytes := len(mutation.Key)
	if len(mutation.Key) > MaximumMutationValueBytes {
		return fmt.Errorf("%w: key exceeds 64 KiB", ErrInvalidBrowserQuery)
	}
	add := func(values ...[]byte) error {
		for _, value := range values {
			if len(value) > MaximumMutationValueBytes {
				return fmt.Errorf("%w: mutation value exceeds 64 KiB", ErrInvalidBrowserQuery)
			}
			bytes += len(value)
		}
		if bytes > maximumMutationBytes {
			return fmt.Errorf("%w: mutation payload exceeds 256 KiB", ErrInvalidBrowserQuery)
		}
		return nil
	}
	switch mutation.Kind {
	case MutationStringSet, MutationListPushLeft, MutationListPushRight, MutationListRemove:
		return add(mutation.Value)
	case MutationHashSet:
		return add(mutation.Field, mutation.Value)
	case MutationHashDelete:
		return add(mutation.Field)
	case MutationListSet:
		if mutation.Index == nil {
			return fmt.Errorf("%w: list_set requires index", ErrInvalidBrowserQuery)
		}
		return add(mutation.Value)
	case MutationSetAdd, MutationSetRemove:
		return add(mutation.Member)
	case MutationZSetAdd:
		if mutation.Score == nil || math.IsNaN(*mutation.Score) || math.IsInf(*mutation.Score, 0) {
			return fmt.Errorf("%w: zset_add requires a finite score", ErrInvalidBrowserQuery)
		}
		return add(mutation.Member)
	case MutationZSetRemove:
		return add(mutation.Member)
	case MutationStreamAdd:
		if len(mutation.Fields) == 0 || len(mutation.Fields) > maximumStreamFields {
			return fmt.Errorf("%w: stream_add requires 1..100 field/value pairs", ErrInvalidBrowserQuery)
		}
		for _, field := range mutation.Fields {
			if err := add(field.Field, field.Value); err != nil {
				return err
			}
		}
		return nil
	case MutationStreamDelete:
		if len(mutation.StreamID) == 0 {
			return fmt.Errorf("%w: stream_delete requires an entry ID", ErrInvalidBrowserQuery)
		}
		return add(mutation.StreamID)
	case MutationTTLSet:
		if mutation.TTLMillis <= 0 {
			return fmt.Errorf("%w: ttl_set requires positive milliseconds", ErrInvalidBrowserQuery)
		}
		return nil
	case MutationTTLClear, MutationKeyDelete:
		return nil
	default:
		return fmt.Errorf("%w: unknown mutation kind", ErrInvalidBrowserQuery)
	}
}

func (client *Client) integerMutation(ctx context.Context, arguments ...string) (MutationResult, error) {
	value, err := client.command(ctx, arguments...)
	if err != nil {
		return MutationResult{}, err
	}
	if value.kind != responseInteger || value.integer < 0 {
		return MutationResult{}, errors.New("Redis mutation returned an invalid count")
	}
	return MutationResult{Affected: value.integer}, nil
}

func (client *Client) expectMutationOK(ctx context.Context, arguments ...string) (MutationResult, error) {
	value, err := client.command(ctx, arguments...)
	if err != nil {
		return MutationResult{}, err
	}
	if value.kind != responseString || value.text != "OK" {
		return MutationResult{}, errors.New("Redis mutation returned an unexpected response")
	}
	return MutationResult{Affected: 1}, nil
}
