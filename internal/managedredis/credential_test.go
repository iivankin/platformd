package managedredis

import (
	"bytes"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/cryptobox"
)

func TestPasswordGenerationAndEncryptionAreResourceBound(t *testing.T) {
	t.Parallel()
	password, err := GeneratePasswordWith(bytes.NewReader(bytes.Repeat([]byte{0x8a}, passwordBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if len(password) != 43 || strings.ContainsAny(password, "+/=") {
		t.Fatalf("generated password has unexpected encoding: %q", password)
	}
	master := cryptobox.MasterKey{1, 2, 3}
	encrypted, err := SealPassword(master, "redis-one", password)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenPassword(master, "redis-one", encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if opened != password {
		t.Fatalf("opened password = %q, want %q", opened, password)
	}
	if _, err := OpenPassword(master, "redis-two", encrypted); err == nil {
		t.Fatal("password opened for another resource")
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := OpenPassword(master, "redis-one", encrypted); err == nil {
		t.Fatal("tampered password opened")
	}
}
