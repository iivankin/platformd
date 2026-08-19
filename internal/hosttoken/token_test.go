package hosttoken

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/iivankin/platformd/internal/id"
)

func TestGenerateAndParseJoin(t *testing.T) {
	t.Parallel()
	publicID, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	value, secret, err := GenerateJoin(publicID, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	parsedID, parsedSecret, err := ParseJoin(value)
	if err != nil {
		t.Fatal(err)
	}
	if parsedID != publicID || parsedSecret != secret {
		t.Fatalf("parsed = %s %s", parsedID, parsedSecret)
	}
	digest := Digest("join", publicID, secret)
	if !Verify("join", publicID, secret, digest) {
		t.Fatal("join digest did not verify")
	}
	if Verify("host", publicID, secret, digest) {
		t.Fatal("join digest verified as host")
	}
}

func TestParseRejectsWrongPrefix(t *testing.T) {
	t.Parallel()
	publicID, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := GenerateHost(publicID, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseJoin(value); err == nil {
		t.Fatal("host token parsed as join token")
	}
	if _, _, err := ParseHost("pht_not-a-cuid2_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("invalid host token was accepted")
	}
}

func TestDigestIsStable(t *testing.T) {
	t.Parallel()
	first := Digest("join", "id", "secret")
	second := Digest("join", "id", "secret")
	if !bytes.Equal(first, second) {
		t.Fatal("digest is not stable")
	}
}
