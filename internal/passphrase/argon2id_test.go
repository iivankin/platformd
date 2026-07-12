package passphrase_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/passphrase"
)

func TestHashAndVerify(t *testing.T) {
	t.Parallel()

	encoded, err := passphrase.HashWith([]byte("correct horse battery staple"), bytes.NewReader(bytes.Repeat([]byte{0x2f}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected verifier parameters: %s", encoded)
	}
	valid, err := passphrase.Verify(encoded, []byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("correct passphrase was rejected")
	}
	valid, err = passphrase.Verify(encoded, []byte("wrong"))
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("wrong passphrase was accepted")
	}
}

func TestVerifyRejectsUnboundedParametersBeforeHashing(t *testing.T) {
	t.Parallel()

	encoded, err := passphrase.HashWith([]byte("value"), bytes.NewReader(bytes.Repeat([]byte{0x3a}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	encoded = strings.Replace(encoded, "m=19456", "m=4294967295", 1)
	if _, err := passphrase.Verify(encoded, []byte("value")); err == nil {
		t.Fatal("unbounded attacker-controlled work factor was accepted")
	}
}
