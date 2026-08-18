package analytics

import "testing"

func TestParseAcquisitionSearchHostsStayOnLabelBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		referrer string
		source   string
		channel  string
	}{
		{"https://www.google.com/search", "Google", "Organic Search"},
		{"https://google.co.uk/", "Google", "Organic Search"},
		{"https://www.yandex.ru/search", "Yandex", "Organic Search"},
		{"https://yandex.com/", "Yandex", "Organic Search"},
		{"https://notgoogle.com/", "notgoogle.com", "Referral"},
		{"https://agoogle.com/", "agoogle.com", "Referral"},
		{"https://fyandex.ru/", "fyandex.ru", "Referral"},
		{"https://www.bing.com/", "Bing", "Organic Search"},
	}
	for _, test := range cases {
		got := ParseAcquisition("https://shop.example/", test.referrer)
		if got.ReferrerSource != test.source || got.Channel != test.channel {
			t.Errorf("%s: source=%q channel=%q, want %s/%s", test.referrer, got.ReferrerSource, got.Channel, test.source, test.channel)
		}
	}
}
