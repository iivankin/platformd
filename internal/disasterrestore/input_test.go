package disasterrestore_test

import (
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/disasterrestore"
	"github.com/iivankin/platformd/internal/masterkey"
)

func TestRestoreInputCanonicalizesTargetAndOptionalAccessOverride(t *testing.T) {
	t.Parallel()
	key := cryptobox.MasterKey{1, 2, 3}
	validated, err := disasterrestore.ValidateInput(disasterrestore.Input{
		MasterRecoveryKey: masterkey.RecoveryString(key), Endpoint: "https://S3.EXAMPLE.com/",
		Region: "region", Bucket: "bucket", Prefix: "/prefix/", AccessKeyID: "access", SecretAccessKey: "secret",
		AccessTeamDomainOverride: "TEAM.cloudflareaccess.com", AccessAudienceOverride: " audience ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if validated.Master != key || validated.Remote.Endpoint != "https://s3.example.com" || validated.Remote.Prefix != "prefix" ||
		validated.AccessTeamDomain == nil || *validated.AccessTeamDomain != "team.cloudflareaccess.com" ||
		validated.AccessAudience == nil || *validated.AccessAudience != "audience" {
		t.Fatalf("validated restore input = %+v", validated)
	}
}

func TestRestoreInputRejectsPartialOverrideAndDuplicateJSONKey(t *testing.T) {
	t.Parallel()
	key := masterkey.RecoveryString(cryptobox.MasterKey{1})
	if _, err := disasterrestore.ValidateInput(disasterrestore.Input{
		MasterRecoveryKey: key, Endpoint: "https://s3.example.com", Region: "region", Bucket: "bucket",
		AccessKeyID: "access", SecretAccessKey: "secret", AccessTeamDomainOverride: "team.cloudflareaccess.com",
	}); err == nil {
		t.Fatal("partial Access override was accepted")
	}
	value := `{"masterRecoveryKey":"` + key + `","masterRecoveryKey":"` + key + `"}`
	if _, err := disasterrestore.ReadInput(strings.NewReader(value)); err == nil {
		t.Fatal("duplicate restore JSON key was accepted")
	}
}
