package cloudflaremesh

import "testing"

func TestParseAddressOutputSelectsMeshAddressAndPreservesNamespace(t *testing.T) {
	address, err := parseAddressOutput(
		"7: CloudflareWARP    inet 100.96.42.7/32 scope global CloudflareWARP\n",
		321,
	)
	if err != nil {
		t.Fatal(err)
	}
	if address.InterfaceName != "CloudflareWARP" || address.Address != "100.96.42.7" || address.NamespacePID != 321 {
		t.Fatalf("parsed address = %+v", address)
	}
}

func TestParseAddressOutputRejectsNonMeshAddress(t *testing.T) {
	if _, err := parseAddressOutput("2: eth0    inet 10.0.0.4/24 scope global eth0\n", 321); err == nil {
		t.Fatal("non-Mesh address was accepted")
	}
}
