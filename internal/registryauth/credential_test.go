package registryauth

import (
	"bytes"
	"testing"

	"github.com/iivankin/platformd/internal/cryptobox"
)

func TestRegistryCredentialRoundTripAndVerifierScope(t *testing.T) {
	credentialID := "018bcfe5-687b-7fff-bfff-ffffffffffff"
	username, err := Username(credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := CredentialID(username); err != nil || parsed != credentialID {
		t.Fatalf("parsed username = %q, %v", parsed, err)
	}
	secret, err := GenerateSecret(bytes.NewReader(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	master := cryptobox.MasterKey{1, 2, 3}
	verifier, err := Verifier(master, "repository", credentialID, secret)
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(master, "repository", credentialID, secret, verifier) || Verify(master, "other", credentialID, secret, verifier) || Verify(master, "repository", credentialID, "wrong", verifier) {
		t.Fatal("registry credential verifier scope is incorrect")
	}
	encrypted, err := SealSecret(master, "repository", credentialID, secret)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenSecret(master, "repository", credentialID, encrypted)
	if err != nil || opened != secret {
		t.Fatalf("opened secret = %q, %v", opened, err)
	}
	if _, err := OpenSecret(master, "other", credentialID, encrypted); err == nil {
		t.Fatal("registry credential encryption was not scoped to its repository")
	}
}
