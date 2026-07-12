package internaldns

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadUpstreamsUsesBoundedUniqueIPv4Nameservers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	value := "search example.test\nnameserver 1.1.1.1\nnameserver 2001:4860:4860::8888\nnameserver 1.1.1.1\nnameserver 8.8.8.8\nnameserver 9.9.9.9\nnameserver 4.4.4.4\n"
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	upstreams, err := ReadUpstreams(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(upstreams) != 3 || upstreams[0].String() != "1.1.1.1:53" || upstreams[2].String() != "9.9.9.9:53" {
		t.Fatalf("unexpected upstreams: %v", upstreams)
	}
}
