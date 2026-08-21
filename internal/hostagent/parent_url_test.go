package hostagent

import "testing"

func TestNormalizeParentURLAllowsHTTPSAndPrivateHTTP(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		input string
		want  string
	}{
		{input: " https://admin.example.com/ ", want: "https://admin.example.com"},
		{input: "http://10.20.0.4", want: "http://10.20.0.4"},
		{input: "http://127.0.0.1:8080/", want: "http://127.0.0.1:8080"},
		{input: "http://[fd00::4]:8080", want: "http://[fd00::4]:8080"},
	} {
		got, err := NormalizeParentURL(test.input)
		if err != nil || got != test.want {
			t.Fatalf("NormalizeParentURL(%q) = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
}

func TestNormalizeParentURLRejectsPublicPlaintextAndNonOrigins(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"http://admin.example.com",
		"http://8.8.8.8",
		"ftp://10.20.0.4",
		"https://admin.example.com/path",
		"https://user@admin.example.com",
	} {
		if _, err := NormalizeParentURL(input); err == nil {
			t.Fatalf("NormalizeParentURL(%q) succeeded", input)
		}
	}
}

func TestParentWebSocketURLMatchesTransportSecurity(t *testing.T) {
	t.Parallel()

	secure, err := parentWebSocketURL("https://admin.example.com", "/connect")
	if err != nil || secure != "wss://admin.example.com/connect" {
		t.Fatalf("secure websocket URL = %q, %v", secure, err)
	}
	private, err := parentWebSocketURL("http://10.20.0.4:8080", "/connect")
	if err != nil || private != "ws://10.20.0.4:8080/connect" {
		t.Fatalf("private websocket URL = %q, %v", private, err)
	}
}
