package automation

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/state"
)

const (
	ManagedResourcePostgres    = "postgres"
	ManagedResourceRedis       = "redis"
	ManagedResourceObjectStore = "object_store"
	managedObjectStoreRegion   = "us-east-1"
)

var ErrManagedResourceInput = errors.New("invalid managed resource query")

type ManagedResourceRepository interface {
	ManagedPostgresByProject(context.Context, string) ([]state.ManagedPostgres, error)
	ManagedPostgresInProject(context.Context, string, string) (state.ManagedPostgres, error)
	ManagedRedisByProject(context.Context, string) ([]state.ManagedRedis, error)
	ManagedRedisInProject(context.Context, string, string) (state.ManagedRedis, error)
	ObjectStoresByProject(context.Context, string) ([]state.ObjectStore, error)
	ObjectStoreInProject(context.Context, string, string) (state.ObjectStore, error)
	BackupHistory(context.Context, state.BackupHistoryQuery) ([]state.BackupRecord, error)
}

type ManagedResourceApplication struct {
	repository ManagedResourceRepository
}

type ManagedResourceMetadata struct {
	Kind                 string   `json:"kind"`
	ID                   string   `json:"id"`
	ProjectID            string   `json:"projectId"`
	Name                 string   `json:"name"`
	InternalHostname     string   `json:"internalHostname"`
	Port                 int      `json:"port,omitempty"`
	ImageTag             string   `json:"imageTag,omitempty"`
	ImageDigest          string   `json:"imageDigest,omitempty"`
	DatabaseName         string   `json:"databaseName,omitempty"`
	OwnerUsername        string   `json:"ownerUsername,omitempty"`
	BucketName           string   `json:"bucketName,omitempty"`
	PublicHostname       string   `json:"publicHostname,omitempty"`
	CORSOrigins          []string `json:"corsOrigins,omitempty"`
	Region               string   `json:"region,omitempty"`
	CPUMillicores        int64    `json:"cpuMillicores,omitempty"`
	MemoryBytes          int64    `json:"memoryBytes,omitempty"`
	BackupEnabled        bool     `json:"backupEnabled"`
	BackupCron           string   `json:"backupCron,omitempty"`
	BackupRetentionCount int      `json:"backupRetentionCount"`
	CreatedAt            int64    `json:"createdAt"`
	UpdatedAt            int64    `json:"updatedAt"`
}

type ManagedResourceBackupRecord struct {
	ID                        string `json:"id"`
	ScheduledOccurrenceMillis *int64 `json:"scheduledOccurrence,omitempty"`
	GenerationID              string `json:"generationId,omitempty"`
	Status                    string `json:"status"`
	SizeBytes                 *int64 `json:"sizeBytes,omitempty"`
	ErrorCode                 string `json:"errorCode,omitempty"`
	ErrorMessage              string `json:"errorMessage,omitempty"`
	StartedAt                 int64  `json:"startedAt"`
	FinishedAt                *int64 `json:"finishedAt,omitempty"`
}

type ManagedResourceBackupStatus struct {
	Resource ManagedResourceMetadata       `json:"resource"`
	Backups  []ManagedResourceBackupRecord `json:"backups"`
}

func NewManagedResourceApplication(repository ManagedResourceRepository) (*ManagedResourceApplication, error) {
	if repository == nil {
		return nil, errors.New("managed resource repository is required")
	}
	return &ManagedResourceApplication{repository: repository}, nil
}

func (application *ManagedResourceApplication) List(
	ctx context.Context,
	identity Identity,
	projectID string,
) ([]ManagedResourceMetadata, error) {
	if err := authorizeManagedResource(identity, projectID); err != nil {
		return nil, err
	}
	postgresResources, err := application.repository.ManagedPostgresByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	redisResources, err := application.repository.ManagedRedisByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	objectStores, err := application.repository.ObjectStoresByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	resources := make([]ManagedResourceMetadata, 0, len(postgresResources)+len(redisResources)+len(objectStores))
	for _, resource := range postgresResources {
		resources = append(resources, publicManagedPostgres(resource))
	}
	for _, resource := range redisResources {
		resources = append(resources, publicManagedRedis(resource))
	}
	for _, resource := range objectStores {
		resources = append(resources, publicManagedObjectStore(resource))
	}
	sort.Slice(resources, func(left, right int) bool {
		if resources[left].Kind != resources[right].Kind {
			return resources[left].Kind < resources[right].Kind
		}
		if resources[left].Name != resources[right].Name {
			return resources[left].Name < resources[right].Name
		}
		return resources[left].ID < resources[right].ID
	})
	return resources, nil
}

func (application *ManagedResourceApplication) Get(
	ctx context.Context,
	identity Identity,
	projectID string,
	kind string,
	resourceID string,
) (ManagedResourceMetadata, error) {
	if err := authorizeManagedResource(identity, projectID); err != nil {
		return ManagedResourceMetadata{}, err
	}
	if resourceID == "" {
		return ManagedResourceMetadata{}, fmt.Errorf("%w: resourceId is required", ErrManagedResourceInput)
	}
	switch kind {
	case ManagedResourcePostgres:
		resource, err := application.repository.ManagedPostgresInProject(ctx, projectID, resourceID)
		return publicManagedPostgres(resource), err
	case ManagedResourceRedis:
		resource, err := application.repository.ManagedRedisInProject(ctx, projectID, resourceID)
		return publicManagedRedis(resource), err
	case ManagedResourceObjectStore:
		resource, err := application.repository.ObjectStoreInProject(ctx, projectID, resourceID)
		return publicManagedObjectStore(resource), err
	default:
		return ManagedResourceMetadata{}, fmt.Errorf("%w: kind must be postgres, redis, or object_store", ErrManagedResourceInput)
	}
}

func (application *ManagedResourceApplication) BackupStatus(
	ctx context.Context,
	identity Identity,
	projectID string,
	kind string,
	resourceID string,
	beforeMillis int64,
	limit int,
) (ManagedResourceBackupStatus, error) {
	resource, err := application.Get(ctx, identity, projectID, kind, resourceID)
	if err != nil {
		return ManagedResourceBackupStatus{}, err
	}
	if beforeMillis < 0 || limit < 1 || limit > 100 {
		return ManagedResourceBackupStatus{}, fmt.Errorf("%w: beforeMillis must be non-negative and limit must be 1..100", ErrManagedResourceInput)
	}
	records, err := application.repository.BackupHistory(ctx, state.BackupHistoryQuery{
		ResourceKind: kind, ResourceID: resourceID, BeforeMillis: beforeMillis, Limit: limit,
	})
	if err != nil {
		return ManagedResourceBackupStatus{}, err
	}
	backups := make([]ManagedResourceBackupRecord, 0, len(records))
	for _, record := range records {
		backups = append(backups, ManagedResourceBackupRecord{
			ID: record.ID, ScheduledOccurrenceMillis: record.ScheduledOccurrenceMillis,
			GenerationID: record.GenerationID, Status: record.Status, SizeBytes: record.SizeBytes,
			ErrorCode: record.ErrorCode, ErrorMessage: record.ErrorMessage,
			StartedAt: record.StartedAtMillis, FinishedAt: record.FinishedAtMillis,
		})
	}
	return ManagedResourceBackupStatus{Resource: resource, Backups: backups}, nil
}

func authorizeManagedResource(identity Identity, projectID string) error {
	if projectID == "" {
		return fmt.Errorf("%w: projectId is required", ErrManagedResourceInput)
	}
	if !identity.AllowsProject(projectID) {
		return ErrProjectBoundary
	}
	return nil
}

func publicManagedPostgres(resource state.ManagedPostgres) ManagedResourceMetadata {
	return ManagedResourceMetadata{
		Kind: ManagedResourcePostgres, ID: resource.ID, ProjectID: resource.ProjectID, Name: resource.Name,
		InternalHostname: resource.Name + "." + resource.ProjectName + ".internal", Port: managedpostgres.Port,
		ImageTag: resource.ImageTag, ImageDigest: resource.ImageDigest,
		DatabaseName: resource.DatabaseName, OwnerUsername: resource.OwnerUsername,
		CPUMillicores: resource.CPUMillicores, MemoryBytes: resource.MemoryMaxBytes,
		BackupEnabled: resource.BackupEnabled, BackupCron: resource.BackupCron,
		BackupRetentionCount: resource.BackupRetentionCount,
		CreatedAt:            resource.CreatedAtMillis, UpdatedAt: resource.UpdatedAtMillis,
	}
}

func publicManagedRedis(resource state.ManagedRedis) ManagedResourceMetadata {
	return ManagedResourceMetadata{
		Kind: ManagedResourceRedis, ID: resource.ID, ProjectID: resource.ProjectID, Name: resource.Name,
		InternalHostname: resource.Name + "." + resource.ProjectName + ".internal", Port: managedredis.Port,
		ImageTag: resource.ImageTag, ImageDigest: resource.ImageDigest,
		CPUMillicores: resource.CPUMillicores, MemoryBytes: resource.MemoryMaxBytes,
		BackupEnabled: resource.BackupEnabled, BackupCron: resource.BackupCron,
		BackupRetentionCount: resource.BackupRetentionCount,
		CreatedAt:            resource.CreatedAtMillis, UpdatedAt: resource.UpdatedAtMillis,
	}
}

func publicManagedObjectStore(resource state.ObjectStore) ManagedResourceMetadata {
	return ManagedResourceMetadata{
		Kind: ManagedResourceObjectStore, ID: resource.ID, ProjectID: resource.ProjectID, Name: resource.Name,
		InternalHostname: resource.Name + "." + resource.ProjectName + ".internal",
		BucketName:       resource.BucketName, PublicHostname: resource.PublicHostname,
		CORSOrigins: append([]string(nil), resource.CORSOrigins...), Region: managedObjectStoreRegion,
		BackupEnabled: resource.BackupEnabled, BackupCron: resource.BackupCron,
		BackupRetentionCount: resource.BackupRetentionCount,
		CreatedAt:            resource.CreatedAtMillis, UpdatedAt: resource.UpdatedAtMillis,
	}
}
