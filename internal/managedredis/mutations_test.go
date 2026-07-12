package managedredis

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"testing"
)

func TestTypeAwareMutationsEmitOnlyFixedRedisCommands(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mutation Mutation
		command  []string
		response string
	}{
		{"string", Mutation{Kind: MutationStringSet, Key: []byte("key"), Value: []byte("value")}, []string{"SET", "key", "value"}, "+OK\r\n"},
		{"hash", Mutation{Kind: MutationHashSet, Key: []byte("key"), Field: []byte("field"), Value: []byte("value")}, []string{"HSET", "key", "field", "value"}, ":1\r\n"},
		{"hash delete", Mutation{Kind: MutationHashDelete, Key: []byte("key"), Field: []byte("field")}, []string{"HDEL", "key", "field"}, ":1\r\n"},
		{"list push left", Mutation{Kind: MutationListPushLeft, Key: []byte("key"), Value: []byte("value")}, []string{"LPUSH", "key", "value"}, ":1\r\n"},
		{"list push right", Mutation{Kind: MutationListPushRight, Key: []byte("key"), Value: []byte("value")}, []string{"RPUSH", "key", "value"}, ":1\r\n"},
		{"list", Mutation{Kind: MutationListSet, Key: []byte("key"), Index: int64Pointer(2), Value: []byte("value")}, []string{"LSET", "key", "2", "value"}, "+OK\r\n"},
		{"list remove", Mutation{Kind: MutationListRemove, Key: []byte("key"), Value: []byte("value"), Count: 1}, []string{"LREM", "key", "1", "value"}, ":1\r\n"},
		{"set add", Mutation{Kind: MutationSetAdd, Key: []byte("key"), Member: []byte("member")}, []string{"SADD", "key", "member"}, ":1\r\n"},
		{"set remove", Mutation{Kind: MutationSetRemove, Key: []byte("key"), Member: []byte("member")}, []string{"SREM", "key", "member"}, ":1\r\n"},
		{"zset", Mutation{Kind: MutationZSetAdd, Key: []byte("key"), Member: []byte("member"), Score: float64Pointer(1.5)}, []string{"ZADD", "key", "1.5", "member"}, ":1\r\n"},
		{"zset remove", Mutation{Kind: MutationZSetRemove, Key: []byte("key"), Member: []byte("member")}, []string{"ZREM", "key", "member"}, ":1\r\n"},
		{"stream", Mutation{Kind: MutationStreamAdd, Key: []byte("key"), Fields: []FieldValue{{Field: []byte("field"), Value: []byte("value")}}}, []string{"XADD", "key", "*", "field", "value"}, "$3\r\n1-0\r\n"},
		{"stream delete", Mutation{Kind: MutationStreamDelete, Key: []byte("key"), StreamID: []byte("1-0")}, []string{"XDEL", "key", "1-0"}, ":1\r\n"},
		{"delete", Mutation{Kind: MutationKeyDelete, Key: []byte("key")}, []string{"DEL", "key"}, ":1\r\n"},
		{"ttl", Mutation{Kind: MutationTTLSet, Key: []byte("key"), TTLMillis: 5000}, []string{"PEXPIRE", "key", "5000"}, ":1\r\n"},
		{"ttl clear", Mutation{Kind: MutationTTLClear, Key: []byte("key")}, []string{"PERSIST", "key"}, ":1\r\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			clientSide, serverSide := net.Pipe()
			defer clientSide.Close()
			defer serverSide.Close()
			client := &Client{connection: clientSide, reader: bufio.NewReader(clientSide)}
			done := make(chan error, 1)
			go func() {
				command, err := readTestCommand(bufio.NewReader(serverSide))
				if err == nil && fmt.Sprint(command) != fmt.Sprint(test.command) {
					err = fmt.Errorf("command = %v, want %v", command, test.command)
				}
				if err == nil {
					_, err = serverSide.Write([]byte(test.response))
				}
				done <- err
			}()
			if _, err := client.Mutate(context.Background(), test.mutation); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMutationRejectsUnboundedValueBeforeNetwork(t *testing.T) {
	t.Parallel()
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := &Client{connection: clientSide, reader: bufio.NewReader(clientSide)}
	if _, err := client.Mutate(context.Background(), Mutation{
		Kind: MutationStringSet, Key: []byte("key"), Value: make([]byte, MaximumMutationValueBytes+1),
	}); err == nil {
		t.Fatal("oversized mutation was accepted")
	}
}

func int64Pointer(value int64) *int64       { return &value }
func float64Pointer(value float64) *float64 { return &value }
