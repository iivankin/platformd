package automationapi

import (
	"context"
	"encoding/json"

	"github.com/iivankin/platformd/internal/registry"
	"github.com/iivankin/platformd/internal/state"
)

type registryApplication interface {
	RepositorySummaries(context.Context) ([]registry.RepositorySummary, error)
	RepositorySummary(context.Context, string) (registry.RepositorySummary, error)
	CreateRepository(context.Context, registry.CreateRepositoryInput) (registry.CreateRepositoryResult, error)
	SetPublicPull(context.Context, registry.SetPublicPullInput) (state.RegistryRepository, string, error)
	Images(context.Context, string, string, int) ([]registry.Image, bool, error)
	Image(context.Context, string, string) (registry.Image, error)
	DeleteTag(context.Context, registry.DeleteInput) (string, string, error)
	DeleteManifest(context.Context, registry.DeleteInput) ([]string, string, error)
	DeleteRepository(context.Context, registry.DeleteInput) (string, error)
	Credentials(context.Context, string) ([]state.RegistryCredential, error)
	CreateCredential(context.Context, registry.CreateCredentialInput) (registry.CreateCredentialResult, error)
	DeleteCredential(context.Context, string, string, registry.Actor) (string, error)
	Cleanup(context.Context, string, bool, registry.Actor) (registry.CleanupResult, error)
}

type registrySettings interface {
	RegistryHostname(context.Context) (string, error)
	SetRegistryHostname(context.Context, state.SetRegistryHostnameInput) (*string, error)
}

type registryRepositoryResponse struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	PublicPull           bool   `json:"publicPull"`
	BackupEnabled        bool   `json:"backupEnabled"`
	BackupCron           string `json:"backupCron,omitempty"`
	BackupRetentionCount int    `json:"backupRetentionCount"`
	CreatedAt            int64  `json:"createdAt"`
	UpdatedAt            int64  `json:"updatedAt"`
	ManifestCount        int    `json:"manifestCount"`
	TagCount             int    `json:"tagCount"`
	BlobCount            int    `json:"blobCount"`
	TotalBlobBytes       int64  `json:"totalBlobBytes"`
	ReferencedBlobBytes  int64  `json:"referencedBlobBytes"`
	LastPushedAt         int64  `json:"lastPushedAt,omitempty"`
	CredentialName       string `json:"credentialName,omitempty"`
	CredentialPermission string `json:"credentialPermission,omitempty"`
	Username             string `json:"username,omitempty"`
	Secret               string `json:"secret,omitempty"`
}

type registryImageResponse struct {
	Digest              string                   `json:"digest"`
	Tags                []string                 `json:"tags"`
	MediaType           string                   `json:"mediaType"`
	Platforms           []registry.ImagePlatform `json:"platforms"`
	PushedAt            int64                    `json:"pushedAt"`
	ManifestSize        int64                    `json:"manifestSize"`
	ReferencedBlobBytes int64                    `json:"referencedBlobBytes"`
	BlobDigests         []string                 `json:"blobDigests"`
	Manifest            json.RawMessage          `json:"manifest,omitempty"`
}

type registryCredentialResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Permission string `json:"permission"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt int64  `json:"lastUsedAt,omitempty"`
	Username   string `json:"username,omitempty"`
	Secret     string `json:"secret,omitempty"`
}

func publicRegistryRepository(repository state.RegistryRepository, created registry.CreateRepositoryResult, summary *registry.RepositorySummary) registryRepositoryResponse {
	result := registryRepositoryResponse{
		ID: repository.ID, Name: repository.Name, PublicPull: repository.PublicPull,
		BackupEnabled: repository.BackupEnabled, BackupCron: repository.BackupCron,
		BackupRetentionCount: repository.BackupRetentionCount,
		CreatedAt:            repository.CreatedAtMillis, UpdatedAt: repository.UpdatedAtMillis,
		CredentialName: created.Credential.Name, CredentialPermission: created.Credential.Permission,
		Username: created.Username, Secret: created.Secret,
	}
	if summary != nil {
		result.ManifestCount = summary.ManifestCount
		result.TagCount = summary.TagCount
		result.BlobCount = summary.BlobCount
		result.TotalBlobBytes = summary.TotalBlobBytes
		result.ReferencedBlobBytes = summary.ReferencedBlobBytes
		result.LastPushedAt = summary.LastPushedAtMillis
	}
	return result
}

func publicRegistryImage(image registry.Image, includeManifest bool) registryImageResponse {
	result := registryImageResponse{
		Digest: image.Digest, Tags: image.Tags, MediaType: image.MediaType, Platforms: image.Platforms,
		PushedAt: image.PushedAtMillis, ManifestSize: image.ManifestSize,
		ReferencedBlobBytes: image.ReferencedBlobBytes, BlobDigests: image.BlobDigests,
	}
	if includeManifest {
		result.Manifest = json.RawMessage(image.ManifestJSON)
	}
	return result
}

func publicRegistryCredential(credential state.RegistryCredential, created registry.CreateCredentialResult) registryCredentialResponse {
	return registryCredentialResponse{
		ID: credential.ID, Name: credential.Name, Permission: credential.Permission,
		CreatedAt: credential.CreatedAtMillis, LastUsedAt: credential.LastUsedAtMillis,
		Username: created.Username, Secret: created.Secret,
	}
}
