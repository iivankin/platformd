package automation

import (
	"context"
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

type managedResourceRepositoryStub struct {
	postgresCalls int
	redisCalls    int
	objectCalls   int
	backupCalls   int
}

func (repository *managedResourceRepositoryStub) ManagedPostgresByProject(context.Context, string) ([]state.ManagedPostgres, error) {
	repository.postgresCalls++
	return []state.ManagedPostgres{{
		ID: "postgres", ProjectID: "project", ProjectName: "shop", Name: "database",
		ImageTag: "18", ImageDigest: "sha256:postgres", DatabaseName: "app", OwnerUsername: "owner",
		OwnerPasswordEncrypted: []byte("must-not-leak"), BootstrapPasswordEncrypted: []byte("must-not-leak"),
		BackupEnabled: true, BackupCron: "0 2 * * *", BackupRetentionCount: 7,
	}}, nil
}

func (repository *managedResourceRepositoryStub) ManagedPostgresInProject(context.Context, string, string) (state.ManagedPostgres, error) {
	repository.postgresCalls++
	return state.ManagedPostgres{
		ID: "postgres", ProjectID: "project", ProjectName: "shop", Name: "database",
		ImageTag: "18", DatabaseName: "app", OwnerUsername: "owner",
		OwnerPasswordEncrypted: []byte("must-not-leak"), BootstrapPasswordEncrypted: []byte("must-not-leak"),
		BackupEnabled: true, BackupRetentionCount: 7,
	}, nil
}

func (repository *managedResourceRepositoryStub) ManagedRedisByProject(context.Context, string) ([]state.ManagedRedis, error) {
	repository.redisCalls++
	return []state.ManagedRedis{{
		ID: "redis", ProjectID: "project", ProjectName: "shop", Name: "cache",
		ImageTag: "8", PasswordEncrypted: []byte("must-not-leak"), BackupRetentionCount: 3,
	}}, nil
}

func (repository *managedResourceRepositoryStub) ManagedRedisInProject(context.Context, string, string) (state.ManagedRedis, error) {
	repository.redisCalls++
	return state.ManagedRedis{
		ID: "redis", ProjectID: "project", ProjectName: "shop", Name: "cache",
		ImageTag: "8", PasswordEncrypted: []byte("must-not-leak"), BackupRetentionCount: 3,
	}, nil
}

func (repository *managedResourceRepositoryStub) ObjectStoresByProject(context.Context, string) ([]state.ObjectStore, error) {
	repository.objectCalls++
	return []state.ObjectStore{{
		ID: "store", ProjectID: "project", ProjectName: "shop", Name: "assets", BucketName: "assets-bucket",
		CORSOrigins: []string{"https://app.example.com"}, BackupRetentionCount: 5,
	}}, nil
}

func (repository *managedResourceRepositoryStub) ObjectStoreInProject(context.Context, string, string) (state.ObjectStore, error) {
	repository.objectCalls++
	return state.ObjectStore{
		ID: "store", ProjectID: "project", ProjectName: "shop", Name: "assets", BucketName: "assets-bucket",
		CORSOrigins: []string{"https://app.example.com"}, BackupRetentionCount: 5,
	}, nil
}

func (repository *managedResourceRepositoryStub) BackupHistory(_ context.Context, query state.BackupHistoryQuery) ([]state.BackupRecord, error) {
	repository.backupCalls++
	return []state.BackupRecord{{
		ID: "backup", ResourceKind: query.ResourceKind, ResourceID: query.ResourceID,
		GenerationID: "generation", Status: "succeeded", StartedAtMillis: 10,
	}}, nil
}

func TestManagedResourceApplicationListsMetadataWithoutSecrets(t *testing.T) {
	repository := &managedResourceRepositoryStub{}
	application, err := NewManagedResourceApplication(repository)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := application.List(context.Background(), Identity{TokenID: "read", Role: "read"}, "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 3 || resources[0].Kind != ManagedResourceObjectStore || resources[1].Kind != ManagedResourcePostgres || resources[2].Kind != ManagedResourceRedis {
		t.Fatalf("managed resources = %+v", resources)
	}
	if resources[0].Region != "us-east-1" || resources[1].DatabaseName != "app" || resources[2].Port != 6379 {
		t.Fatalf("managed resource metadata = %+v", resources)
	}
	if repository.postgresCalls != 1 || repository.redisCalls != 1 || repository.objectCalls != 1 {
		t.Fatalf("list calls = postgres:%d redis:%d object:%d", repository.postgresCalls, repository.redisCalls, repository.objectCalls)
	}
}

func TestManagedResourceApplicationEnforcesBoundaryBeforeLookupAndReadsBackups(t *testing.T) {
	repository := &managedResourceRepositoryStub{}
	application, err := NewManagedResourceApplication(repository)
	if err != nil {
		t.Fatal(err)
	}
	bound := "project"
	identity := Identity{TokenID: "read", Role: "read", ProjectID: &bound}
	if _, err := application.Get(context.Background(), identity, "other", ManagedResourceRedis, "redis"); !errors.Is(err, ErrProjectBoundary) {
		t.Fatalf("cross-project get error = %v", err)
	}
	if repository.redisCalls != 0 {
		t.Fatalf("cross-project get performed %d repository calls", repository.redisCalls)
	}
	status, err := application.BackupStatus(context.Background(), identity, "project", ManagedResourcePostgres, "postgres", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if status.Resource.ID != "postgres" || len(status.Backups) != 1 || status.Backups[0].GenerationID != "generation" || repository.backupCalls != 1 {
		t.Fatalf("backup status = %+v calls=%d", status, repository.backupCalls)
	}
}
