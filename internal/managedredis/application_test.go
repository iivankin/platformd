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
	audit state.RecordManagedRedisDataMutation
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

func (store *applicationStore) RecordManagedRedisDataMutation(_ context.Context, input state.RecordManagedRedisDataMutation) error {
	store.audit = input
	return nil
}

func (*applicationStore) RuntimeDeployments(context.Context, string, string, string, int) (state.RuntimeDeploymentPage, error) {
	return state.RuntimeDeploymentPage{}, nil
}

func (*applicationStore) RuntimeDeployment(context.Context, string, string, string) (state.RuntimeDeployment, error) {
	return state.RuntimeDeployment{}, nil
}

type applicationRuntime struct {
	tag           string
	startedID     string
	persistenceID string
	persistence   PersistenceStatus
	mutation      Mutation
	mutatedID     string
}

func (runtime *applicationRuntime) ResolveManagedRedisImage(_ context.Context, tag string) (string, error) {
	runtime.tag = tag
	return testImageDigest, nil
}

func (runtime *applicationRuntime) StartManagedRedis(_ context.Context, id string) error {
	runtime.startedID = id
	return nil
}

func (*applicationRuntime) RestartManagedRedisDeployment(context.Context, string, string) error {
	return nil
}

func (*applicationRuntime) RemoveManagedRedisDeployment(context.Context, string, string) error {
	return nil
}

func (runtime *applicationRuntime) ManagedRedisPersistence(_ context.Context, id string) (PersistenceStatus, error) {
	runtime.persistenceID = id
	return runtime.persistence, nil
}

func (*applicationRuntime) ManagedRedisStats(context.Context, string) (Stats, error) {
	return Stats{}, nil
}

func (*applicationRuntime) ScanManagedRedisKeys(context.Context, string, ScanQuery) (KeyPage, error) {
	return KeyPage{}, nil
}

func (*applicationRuntime) PreviewManagedRedisKey(context.Context, string, PreviewQuery) (Preview, error) {
	return Preview{}, nil
}

func (runtime *applicationRuntime) MutateManagedRedis(_ context.Context, id string, mutation Mutation) (MutationResult, error) {
	runtime.mutatedID = id
	runtime.mutation = mutation
	return MutationResult{Affected: 1}, nil
}

func TestApplicationReportsLiveRedisRPOWithoutDurableState(t *testing.T) {
	t.Parallel()
	runtime := &applicationRuntime{persistence: PersistenceStatus{
		LastBackgroundSaveOK: true, LastSuccessfulSaveUnixSeconds: 1_700_000_000,
	}}
	application, err := NewApplication(
		&applicationStore{}, runtime, cryptobox.MasterKey{1}, nil,
		func() time.Time { return time.UnixMilli(1_700_000_600_000) },
	)
	if err != nil {
		t.Fatal(err)
	}
	report, err := application.Persistence(context.Background(), "project", "redis")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.persistenceID != "redis" || report.ActualRPOMillis != 10*time.Minute.Milliseconds() ||
		report.TargetRPOMillis != TargetRPO.Milliseconds() || !report.NeedsAttention ||
		report.LastSuccessfulSaveAtMillis != 1_700_000_000_000 || !report.LastBackgroundSaveSuccessful {
		t.Fatalf("persistence runtime/report = %+v/%+v", runtime, report)
	}
}

func TestApplicationRestrictsDataMutationsToAccessAndAuditsWithoutContent(t *testing.T) {
	t.Parallel()
	store := &applicationStore{}
	runtime := &applicationRuntime{}
	application, err := NewApplication(
		store, runtime, cryptobox.MasterKey{1}, bytes.NewReader(bytes.Repeat([]byte{0x24}, 128)),
		func() time.Time { return time.UnixMilli(1_700_000_000_000) },
	)
	if err != nil {
		t.Fatal(err)
	}
	mutation := Mutation{Kind: MutationStringSet, Key: []byte("secret-key"), Value: []byte("secret-value")}
	if _, err := application.Mutate(context.Background(), DataMutationInput{
		ProjectID: "project", ResourceID: "redis", Actor: Actor{Kind: "token", ID: "token"}, Mutation: mutation,
	}); err == nil {
		t.Fatal("token actor was allowed to mutate Redis data")
	}
	result, err := application.Mutate(context.Background(), DataMutationInput{
		ProjectID: "project", ResourceID: "redis",
		Actor: Actor{Kind: "access", ID: "user", Email: "user@example.com"}, Mutation: mutation,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Affected != 1 || !result.AuditRecorded || result.RequestID == "" || runtime.mutatedID != "redis" {
		t.Fatalf("mutation result/runtime = %+v/%+v", result, runtime)
	}
	if store.audit.Operation != "string_set" || store.audit.ActorEmail != "user@example.com" || store.audit.Result != "succeeded" {
		t.Fatalf("mutation audit = %+v", store.audit)
	}
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
		MemoryBytes: 128 << 20,
		Credentials: &InitialCredentials{Password: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		Actor:       Actor{Kind: "access", ID: "user", Email: "user@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Resource.ID == "" || result.Resource.VolumeID == "" || result.RequestID == "" || result.Password != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
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
