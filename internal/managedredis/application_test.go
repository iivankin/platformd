package managedredis

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

type applicationStore struct {
	input state.CreateManagedRedis
}

func (store *applicationStore) CreateManagedRedis(_ context.Context, input state.CreateManagedRedis) (state.ManagedRedis, error) {
	store.input = input
	return state.ManagedRedis{
		ID: input.ID, ProjectID: input.ProjectID, Name: input.Name, ImageTag: input.ImageTag,
		ImageDigest: input.ImageDigest, VolumeID: input.VolumeID,
		PasswordEncrypted: input.PasswordEncrypted,
	}, nil
}

func (*applicationStore) ManagedRedisInProject(context.Context, string, string) (state.ManagedRedis, error) {
	return state.ManagedRedis{}, nil
}

func (*applicationStore) ManagedRedisByProject(context.Context, string) ([]state.ManagedRedis, error) {
	return nil, nil
}

type applicationRuntime struct {
	tag       string
	startedID string
}

func (runtime *applicationRuntime) ResolveManagedRedisImage(_ context.Context, tag string) (string, error) {
	runtime.tag = tag
	return testImageDigest, nil
}

func (runtime *applicationRuntime) StartManagedRedis(_ context.Context, id string) error {
	runtime.startedID = id
	return nil
}

func TestApplicationPinsImageEncryptsPasswordAndStartsDurableResource(t *testing.T) {
	t.Parallel()
	store := &applicationStore{}
	runtime := &applicationRuntime{}
	master := cryptobox.MasterKey{1, 2, 3}
	random := bytes.NewReader(bytes.Repeat([]byte{0x42}, 256))
	application, err := NewApplication(store, runtime, master, random, func() time.Time {
		return time.UnixMilli(1_700_000_000_000)
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := application.Create(context.Background(), CreateInput{
		ProjectID: "project", Name: "cache", ImageTag: "7.4", CPUMillicores: 250,
		MemoryBytes: 128 << 20, Actor: Actor{Kind: "access", ID: "user", Email: "user@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Resource.ID == "" || result.Resource.VolumeID == "" || result.RequestID == "" || result.Password == "" {
		t.Fatalf("incomplete create result: %+v", result)
	}
	if runtime.tag != "7.4" || runtime.startedID != result.Resource.ID || store.input.ImageDigest != testImageDigest {
		t.Fatalf("runtime/store mismatch: runtime=%+v store=%+v", runtime, store.input)
	}
	opened, err := OpenPassword(master, result.Resource.ID, store.input.PasswordEncrypted)
	if err != nil || opened != result.Password {
		t.Fatalf("stored password does not open: %q, %v", opened, err)
	}
	if store.input.ActorKind != "access" || store.input.ActorEmail != "user@example.com" || store.input.AuditEventID == "" {
		t.Fatalf("audit identity was not propagated: %+v", store.input)
	}
}
