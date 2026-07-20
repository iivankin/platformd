package containerengine

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseProcListeningPortsFiltersStateAndLoopback(t *testing.T) {
	t.Parallel()
	table := `  sl  local_address rem_address   st
   0: 00000000:1F90 00000000:0000 0A
   1: 0100007F:0BB8 00000000:0000 0A
   2: 020011AC:2382 00000000:0000 01
`

	ports, err := parseProcListeningPorts(strings.NewReader(table), procSocketTable{
		protocol: "tcp", state: "0A",
	})

	if err != nil {
		t.Fatal(err)
	}
	want := []ListeningPort{{Port: 8080, Protocol: "tcp"}}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports = %#v, want %#v", ports, want)
	}
}

func TestParseProcListeningPortsFiltersIPv6Loopback(t *testing.T) {
	t.Parallel()
	table := `  sl  local_address rem_address   st
   0: 00000000000000000000000000000000:14E9 00000000000000000000000000000000:0000 07
   1: 00000000000000000000000001000000:14EA 00000000000000000000000000000000:0000 07
`

	ports, err := parseProcListeningPorts(strings.NewReader(table), procSocketTable{
		protocol: "udp", state: "07",
	})

	if err != nil {
		t.Fatal(err)
	}
	want := []ListeningPort{{Port: 5353, Protocol: "udp"}}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports = %#v, want %#v", ports, want)
	}
}

func TestUniqueListeningPortsSortsAndDeduplicates(t *testing.T) {
	t.Parallel()
	ports := uniqueListeningPorts([]ListeningPort{
		{Port: 8080, Protocol: "tcp"},
		{Port: 53, Protocol: "udp"},
		{Port: 53, Protocol: "tcp"},
		{Port: 8080, Protocol: "tcp"},
	})

	want := []ListeningPort{
		{Port: 53, Protocol: "tcp"},
		{Port: 53, Protocol: "udp"},
		{Port: 8080, Protocol: "tcp"},
	}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports = %#v, want %#v", ports, want)
	}
}
