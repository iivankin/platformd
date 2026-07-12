package apitoken_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/apitoken"
	"github.com/iivankin/platformd/internal/cryptobox"
)

func TestTokenFormatAndConstantTimeVerifierMaterial(t *testing.T) {
	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := apitoken.NewVerifier(master)
	if err != nil {
		t.Fatal(err)
	}
	const publicID = "018bcfe5-687b-7fff-bfff-ffffffffffff"
	value, secret, err := apitoken.Generate(publicID, bytes.NewReader(bytes.Repeat([]byte{0x17}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	parsedID, parsedSecret, err := apitoken.Parse(value)
	if err != nil || parsedID != publicID || parsedSecret != secret || !strings.HasPrefix(value, "ptk_") {
		t.Fatalf("parsed token = %q/%q, %v", parsedID, parsedSecret, err)
	}
	digest := verifier.Digest(publicID, secret)
	if !verifier.Verify(publicID, secret, digest) || verifier.Verify(publicID, secret+"x", digest) {
		t.Fatal("API token verifier accepted the wrong secret")
	}
	if _, _, err := apitoken.Parse(value + "="); err == nil {
		t.Fatal("non-canonical token secret was accepted")
	}
}
