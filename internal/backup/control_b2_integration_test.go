//go:build integration

package backup_test

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/masterkey"
	"github.com/iivankin/platformd/internal/releaseconfig"
	"github.com/iivankin/platformd/internal/remotes3"
)

func TestControlB2Integration(t *testing.T) {
	if os.Getenv("PLATFORMD_CONTROL_B2_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_CONTROL_B2_INTEGRATION=1 with an isolated backup target")
	}

	required := func(name string) string {
		t.Helper()
		value := os.Getenv(name)
		if value == "" {
			t.Fatalf("%s is required", name)
		}
		return value
	}
	master, err := masterkey.ParseRecoveryString(required("PLATFORMD_CONTROL_MASTER_RECOVERY_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for index := range master {
			master[index] = 0
		}
	}()
	remote, err := remotes3.New(remotes3.Config{
		Endpoint:        required("PLATFORMD_CONTROL_S3_ENDPOINT"),
		Region:          required("PLATFORMD_CONTROL_S3_REGION"),
		Bucket:          required("PLATFORMD_CONTROL_S3_BUCKET"),
		Prefix:          required("PLATFORMD_CONTROL_S3_PREFIX"),
		AccessKeyID:     required("PLATFORMD_CONTROL_S3_ACCESS_KEY_ID"),
		SecretAccessKey: required("PLATFORMD_CONTROL_S3_SECRET_ACCESS_KEY"),
	})
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := releaseconfig.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := backup.FetchControl(context.Background(), backup.ControlFetchConfig{
		Remote: remote, Master: master, WorkRoot: t.TempDir(), ExpectedUID: os.Geteuid(),
		PublicKey: publicKey, OS: "linux", Architecture: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(fetched.WorkDirectory)

	if expected := os.Getenv("PLATFORMD_CONTROL_EXPECTED_VERSION"); expected != "" && fetched.Manifest.PlatformVersion != expected {
		t.Fatalf("control platform version = %s, want %s", fetched.Manifest.PlatformVersion, expected)
	}
	if expected := os.Getenv("PLATFORMD_CONTROL_EXPECTED_VOLUME_ID"); expected != "" && !slices.Contains(fetched.Manifest.Resources.Volumes, expected) {
		t.Fatalf("control snapshot does not contain expected ordinary volume")
	}
	t.Logf(
		"verified control generation %s: platform=%s schema=%d resources=%d/%d/%d/%d/%d",
		fetched.Manifest.GenerationID,
		fetched.Manifest.PlatformVersion,
		fetched.Manifest.SchemaVersion,
		len(fetched.Manifest.Resources.RegistryRepositories),
		len(fetched.Manifest.Resources.ObjectStores),
		len(fetched.Manifest.Resources.Postgres),
		len(fetched.Manifest.Resources.Redis),
		len(fetched.Manifest.Resources.Volumes),
	)
}
