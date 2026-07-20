package managedpostgres

import (
	"bytes"
	"testing"

	"github.com/iivankin/platformd/internal/cryptobox"
)

func TestCredentialsRoundTripWithSeparatedEncryptionDomains(t *testing.T) {
	t.Parallel()
	resourceID := "018bcfe5-687b-7fff-bfff-ffffffffffff"
	credentials, err := GenerateCredentials(resourceID, bytes.NewReader(bytes.Repeat([]byte{7}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	master := cryptobox.MasterKey(bytes.Repeat([]byte{9}, 32))
	owner, err := SealOwnerPassword(master, resourceID, credentials.OwnerPassword)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := SealBootstrapPassword(master, resourceID, credentials.BootstrapPassword)
	if err != nil {
		t.Fatal(err)
	}
	openedOwner, err := OpenOwnerPassword(master, resourceID, owner)
	if err != nil || openedOwner != credentials.OwnerPassword {
		t.Fatalf("owner round trip = %q, %v", openedOwner, err)
	}
	openedBootstrap, err := OpenBootstrapPassword(master, resourceID, bootstrap)
	if err != nil || openedBootstrap != credentials.BootstrapPassword {
		t.Fatalf("bootstrap round trip = %q, %v", openedBootstrap, err)
	}
	if _, err := OpenOwnerPassword(master, resourceID, bootstrap); err == nil {
		t.Fatal("bootstrap ciphertext opened in owner domain")
	}
}

func TestDraftCredentialsAcceptGeneratedValues(t *testing.T) {
	t.Parallel()
	credentials := InitialCredentials{
		DatabaseName:  "app_018bcfe5687b7fffbfffffff",
		OwnerUsername: "owner_018bcfe5687b7fffbfffffff",
		OwnerPassword: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
	if err := validateInitialCredentials(credentials); err != nil {
		t.Fatal(err)
	}
	credentials.OwnerUsername = "owner-invalid!"
	if err := validateInitialCredentials(credentials); err == nil {
		t.Fatal("invalid draft username was accepted")
	}
}
