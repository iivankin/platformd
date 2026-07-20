package portforwardclient

import "testing"

func TestValidateKeepsTicketOutOfInsecureOrDecoratedURLs(t *testing.T) {
	valid := Config{
		URL:    "wss://automation.example.com/api/v1/port-forward",
		Ticket: "pft_secret", LocalPort: 5432,
	}
	if err := validate(valid); err != nil {
		t.Fatal(err)
	}
	tests := []Config{
		{URL: "ws://automation.example.com/api/v1/port-forward", Ticket: valid.Ticket, LocalPort: valid.LocalPort},
		{URL: valid.URL + "?ticket=pft_secret", Ticket: valid.Ticket, LocalPort: valid.LocalPort},
		{URL: valid.URL, LocalPort: valid.LocalPort},
		{URL: valid.URL, Ticket: valid.Ticket, LocalPort: 0},
	}
	for _, test := range tests {
		if err := validate(test); err == nil {
			t.Fatalf("accepted unsafe configuration: %+v", test)
		}
	}
}
