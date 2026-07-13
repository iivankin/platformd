package server_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

type successfulBackupProbe struct{}

func (successfulBackupProbe) Probe(context.Context) error {
	return nil
}

type manualBackupRunner struct {
	record state.BackupRecord
	err    error
}

func (runner manualBackupRunner) TryRunNow(context.Context, string, string, int) (state.BackupRecord, error) {
	return runner.record, runner.err
}

func TestBackupTargetAccessOnlyAPIProbesAndNeverReturnsSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateInstallation(ctx, state.InitialInstallation{
		ID: "installation", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "$argon2id$verifier", OriginCertificateID: "certificate",
		OriginCertificatePEM: "certificate", OriginPrivateKey: []byte("encrypted"),
		InitialAuditEventID: "initial-audit", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	probeCalls := 0
	application, err := backup.NewTargetApplication(
		store, cryptobox.MasterKey{1, 2, 3}, backup.NewGate(),
		func(config remotes3.Config) (backup.Probe, error) {
			probeCalls++
			if config.SecretAccessKey != "remote-secret" {
				t.Fatalf("probe secret = %q", config.SecretAccessKey)
			}
			return successfulBackupProbe{}, nil
		},
		bytes.NewReader(serverSequenceBytes(100)),
		func() time.Time { return time.UnixMilli(1_720_000_000_000) },
	)
	if err != nil {
		t.Fatal(err)
	}
	raw := server.Handler(server.DefaultMeta("ready"), server.WithBackupTargets(application))
	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, raw)

	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, projectRequest(http.MethodGet, "/api/v1/backups/target", ""))
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"configured":false`) {
		t.Fatalf("unconfigured target = %d/%s", getResponse.Code, getResponse.Body)
	}

	put := projectRequest(http.MethodPut, "/api/v1/backups/target", `{
  "endpoint":"https://s3.example.com",
  "region":"eu-central-003",
  "bucket":"backup-bucket",
  "prefix":"platformd/test",
  "accessKeyId":"remote-access",
  "secretAccessKey":"remote-secret"
}`)
	put.Header.Set("Origin", "https://admin.example.com")
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusOK || probeCalls != 1 ||
		!strings.Contains(putResponse.Body.String(), `"configured":true`) ||
		strings.Contains(putResponse.Body.String(), "remote-secret") {
		t.Fatalf("set target = %d/%s probeCalls=%d", putResponse.Code, putResponse.Body, probeCalls)
	}

	getResponse = httptest.NewRecorder()
	handler.ServeHTTP(getResponse, projectRequest(http.MethodGet, "/api/v1/backups/target", ""))
	if getResponse.Code != http.StatusOK || strings.Contains(getResponse.Body.String(), "remote-secret") ||
		!strings.Contains(getResponse.Body.String(), `"accessKeyId":"remote-access"`) {
		t.Fatalf("configured target = %d/%s", getResponse.Code, getResponse.Body)
	}

	deleteRequest := projectRequest(http.MethodDelete, "/api/v1/backups/target", "")
	deleteRequest.Header.Set("Origin", "https://admin.example.com")
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete target = %d/%s", deleteResponse.Code, deleteResponse.Body)
	}
}

func TestBackupResourcePolicyHistoryAndImmediateRunAPI(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "demo", AuditEventID: "project-audit",
		ActorID: "user", ActorEmail: "user@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManagedRedis(ctx, state.CreateManagedRedis{
		ID: "redis", ProjectID: "project", Name: "cache", ImageTag: "7.4",
		ImageDigest: "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4",
		VolumeID:    "volume", PasswordEncrypted: []byte("sealed"), AuditEventID: "redis-audit",
		ActorKind: "access", ActorID: "user", ActorEmail: "user@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	application, err := backup.NewResourceApplication(store, manualBackupRunner{record: state.BackupRecord{
		ID: "backup", ResourceKind: "redis", ResourceID: "redis", GenerationID: "generation",
		Status: "running", StartedAtMillis: 20,
	}}, bytes.NewReader(serverSequenceBytes(100)), func() time.Time { return time.UnixMilli(10) })
	if err != nil {
		t.Fatal(err)
	}
	raw := server.Handler(server.DefaultMeta("ready"), server.WithBackupResources(application))
	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, raw)
	put := projectRequest(http.MethodPut, "/api/v1/backups/resources/redis/redis/policy", `{
  "enabled":true,
  "cron":" 5 */2 * * 1-5 ",
  "retentionCount":12
}`)
	put.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, put)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cron":"5 */2 * * 1-5"`) ||
		!strings.Contains(response.Body.String(), `"retentionCount":12`) || response.Header().Get("X-Request-ID") == "" {
		t.Fatalf("set backup policy = %d/%s headers=%v", response.Code, response.Body, response.Header())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/backups/resources", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"resourceId":"redis"`) {
		t.Fatalf("list backup policies = %d/%s", response.Code, response.Body)
	}
	run := projectRequest(http.MethodPost, "/api/v1/backups/resources/redis/redis/run", "")
	run.Header.Set("Origin", "https://admin.example.com")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, run)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"status":"running"`) {
		t.Fatalf("run backup now = %d/%s", response.Code, response.Body)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/backups/resources/redis/redis/history?limit=50", ""))
	if response.Code != http.StatusOK || response.Body.String() != "{\"backups\":[]}\n" {
		t.Fatalf("backup history = %d/%s", response.Code, response.Body)
	}
}

func serverSequenceBytes(count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = byte(index)
	}
	return result
}
