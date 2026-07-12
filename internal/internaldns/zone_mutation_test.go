package internaldns

import (
	"net/netip"
	"testing"
)

func TestZoneSetAndDeleteUpdateOneRecord(t *testing.T) {
	zone, err := NewZone(map[string]netip.Addr{
		"api.shop.internal": netip.MustParseAddr("10.80.0.2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := zone.Set("worker.shop.internal", netip.MustParseAddr("10.80.0.3")); err != nil {
		t.Fatal(err)
	}
	if address, ok := zone.lookup("worker.shop.internal."); !ok || address != [4]byte{10, 80, 0, 3} {
		t.Fatalf("worker lookup = %v/%v", address, ok)
	}
	if err := zone.Delete("api.shop.internal"); err != nil {
		t.Fatal(err)
	}
	if _, ok := zone.lookup("api.shop.internal."); ok {
		t.Fatal("deleted record remains visible")
	}
}
