package internaldns

import "testing"

func TestParseInternalName(t *testing.T) {
	resource, project, ok := ParseInternalName("db.shop.internal")
	if !ok || resource != "db" || project != "shop" {
		t.Fatalf("db.shop.internal = %q %q %v", resource, project, ok)
	}
	resource, project, ok = ParseInternalName("errors-api.shop.internal.")
	if !ok || resource != "errors-api" || project != "shop" {
		t.Fatalf("errors-api = %q %q %v", resource, project, ok)
	}
	if _, _, ok := ParseInternalName("shop.internal"); ok {
		t.Fatal("bare project name must not parse")
	}
	if _, _, ok := ParseInternalName("example.com"); ok {
		t.Fatal("public names must not parse")
	}
}
