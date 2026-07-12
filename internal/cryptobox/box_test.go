package cryptobox_test

import (
	"bytes"
	"testing"

	"github.com/iivankin/platformd/internal/cryptobox"
)

func TestSealRoundTripAndAuthentication(t *testing.T) {
	t.Parallel()

	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x17}, 32))
	if err != nil {
		t.Fatal(err)
	}
	box, err := cryptobox.NewBox(master, []byte("resource-7"), "platformd/test/v1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.SealWith(bytes.NewReader(bytes.Repeat([]byte{0x42}, 24)), []byte("secret"), []byte("resource-7:value"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := box.Open(sealed, []byte("resource-7:value"))
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != "secret" {
		t.Fatalf("opened = %q", opened)
	}

	sealed[len(sealed)-1] ^= 1
	if _, err := box.Open(sealed, []byte("resource-7:value")); err == nil {
		t.Fatal("tampered ciphertext authenticated")
	}
}

func TestDomainAndAssociatedDataCannotBeSubstituted(t *testing.T) {
	t.Parallel()

	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatal(err)
	}
	first, err := cryptobox.NewBox(master, []byte("one"), "platformd/secret/v1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := cryptobox.NewBox(master, []byte("two"), "platformd/secret/v1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := first.SealWith(bytes.NewReader(bytes.Repeat([]byte{0x44}, 24)), []byte("secret"), []byte("one:value"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Open(sealed, []byte("one:value")); err == nil {
		t.Fatal("ciphertext opened under a different resource salt")
	}
	if _, err := first.Open(sealed, []byte("one:other-field")); err == nil {
		t.Fatal("ciphertext opened under different associated data")
	}
}
