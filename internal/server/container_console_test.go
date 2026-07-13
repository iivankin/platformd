package server

import (
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestContainerTerminalCommandSupportsAllowlistedShellAndExplicitArgv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want []string
		err  bool
	}{
		{path: "/terminal", want: []string{"/bin/sh"}},
		{path: "/terminal?shell=bash", want: []string{"/bin/bash"}},
		{path: "/terminal?arg=%2Fusr%2Fbin%2Fenv&arg=sh", want: []string{"/usr/bin/env", "sh"}},
		{path: "/terminal?shell=zsh", err: true},
		{path: "/terminal?shell=sh&arg=%2Fbin%2Fsh", err: true},
	}
	for _, test := range tests {
		request := httptest.NewRequest("GET", test.path, nil)
		got, err := containerTerminalCommand(request)
		if (err != nil) != test.err || !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s command = %#v, %v", test.path, got, err)
		}
	}
}

func TestTerminalSourceIPPrefersSingleCloudflareAddress(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest("GET", "/terminal", nil)
	request.RemoteAddr = "192.0.2.4:1234"
	request.Header.Set("CF-Connecting-IP", "203.0.113.7")
	if address, err := terminalSourceIP(request); err != nil || address != "203.0.113.7" {
		t.Fatalf("source IP = %q, %v", address, err)
	}
	request.Header.Set("CF-Connecting-IP", "203.0.113.7, 198.51.100.8")
	if _, err := terminalSourceIP(request); err == nil {
		t.Fatal("multiple source addresses were accepted")
	}
}
