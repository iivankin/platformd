package objectstore

import "testing"

func TestAccessKeyCUID2RoundTrip(t *testing.T) {
	t.Parallel()

	const credentialID = "abcdefghijklmnopqrstuvwx"
	accessKey, err := AccessKeyID(credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if accessKey != "ps3_"+credentialID {
		t.Fatalf("access key = %q", accessKey)
	}
	parsed, err := CredentialID(accessKey)
	if err != nil || parsed != credentialID {
		t.Fatalf("parsed credential ID = %q, %v", parsed, err)
	}
	if _, err := AccessKeyID("018bcfe5-687b-7fff-bfff-ffffffffffff"); err == nil {
		t.Fatal("legacy UUID credential ID was accepted")
	}
}
