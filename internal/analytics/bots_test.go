package analytics

import "testing"

func TestParseUserAgentReadsOSVersion(t *testing.T) {
	t.Parallel()
	iphone := ParseUserAgent("Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15 Version/17.4 Mobile/15E148 Safari/604.1")
	if iphone.OS != "iOS" || iphone.OSVersion != "17.4" || iphone.Browser != "Safari" {
		t.Fatalf("iphone = %+v", iphone)
	}
	android := ParseUserAgent("Mozilla/5.0 (Linux; Android 14; Pixel) AppleWebKit/537.36 Chrome/120.0.0.0 Mobile Safari/537.36")
	if android.OS != "Android" || android.OSVersion != "14" || android.Device != "Mobile" {
		t.Fatalf("android = %+v", android)
	}
}

func TestClassifyBotKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ua   string
		kind string
		name string
	}{
		{"Mozilla/5.0 GPTBot", "training", "GPTBot"},
		{"ClaudeBot/1.0", "training", "ClaudeBot"},
		{"OAI-SearchBot", "search", "OAI-SearchBot"},
		{"Bytespider", "search", "Bytespider"},
		{"ChatGPT-User", "fetch", "ChatGPT-User"},
		{"Google-Extended", "", ""},
		{"Applebot-Extended", "", ""},
		{"Mozilla/5.0 Chrome/120", "", ""},
	}
	for _, test := range cases {
		got := ClassifyBot(test.ua)
		if got.Kind != test.kind || got.Name != test.name {
			t.Errorf("ClassifyBot(%q) = %+v, want %s/%s", test.ua, got, test.kind, test.name)
		}
	}
}
