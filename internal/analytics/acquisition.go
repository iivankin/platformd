package analytics

import (
	"net/url"
	"strings"
)

type Acquisition struct {
	Referrer       string
	ReferrerDomain string
	ReferrerSource string
	Channel        string
	UTMSource      string
	UTMMedium      string
	UTMCampaign    string
	UTMContent     string
	UTMTerm        string
	Gclid          string
	Fbclid         string
	Msclkid        string
	Ttclid         string
	LiFatID        string
	Twclid         string
}

var searchHosts = map[string]string{
	"google.com": "Google", "google.": "Google", "bing.com": "Bing", "duckduckgo.com": "DuckDuckGo",
	"yahoo.com": "Yahoo", "baidu.com": "Baidu", "yandex.": "Yandex",
}
var socialHosts = map[string]string{
	"facebook.com": "Facebook", "instagram.com": "Instagram", "twitter.com": "Twitter",
	"x.com": "Twitter", "linkedin.com": "LinkedIn", "reddit.com": "Reddit",
	"tiktok.com": "TikTok", "youtube.com": "YouTube",
}
var aiHosts = map[string]string{
	"chatgpt.com": "ChatGPT", "chat.openai.com": "ChatGPT", "perplexity.ai": "Perplexity",
	"claude.ai": "Claude", "gemini.google.com": "Gemini", "copilot.microsoft.com": "Copilot",
}

func ParseAcquisition(pageURL, referrer string) Acquisition {
	parsedPage, _ := url.Parse(pageURL)
	query := url.Values{}
	if parsedPage != nil {
		query = parsedPage.Query()
	}
	acquisition := Acquisition{
		Referrer:    referrer,
		UTMSource:   first(query, "utm_source"),
		UTMMedium:   first(query, "utm_medium"),
		UTMCampaign: first(query, "utm_campaign"),
		UTMContent:  first(query, "utm_content"),
		UTMTerm:     first(query, "utm_term"),
		Gclid:       first(query, "gclid"),
		Fbclid:      first(query, "fbclid"),
		Msclkid:     first(query, "msclkid"),
		Ttclid:      first(query, "ttclid"),
		LiFatID:     first(query, "li_fat_id"),
		Twclid:      first(query, "twclid"),
	}
	if referrer != "" {
		if parsed, err := url.Parse(referrer); err == nil {
			acquisition.ReferrerDomain = strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
		}
	}
	acquisition.ReferrerSource, acquisition.Channel = classifyChannel(acquisition)
	return acquisition
}

func classifyChannel(acquisition Acquisition) (string, string) {
	medium := strings.ToLower(acquisition.UTMMedium)
	source := strings.ToLower(acquisition.UTMSource)
	switch {
	case acquisition.Gclid != "" || medium == "cpc" || medium == "ppc" || medium == "paidsearch":
		return named(source, "Google"), "Paid Search"
	case acquisition.Fbclid != "" || medium == "paidsocial":
		return named(source, "Facebook"), "Paid Social"
	case medium == "email" || source == "email":
		return named(source, "Email"), "Email"
	case medium == "affiliate":
		return named(source, "Affiliate"), "Affiliate"
	}
	if host := acquisition.ReferrerDomain; host != "" {
		if name := hostSource(host, aiHosts); name != "" {
			return name, "AI Assistants"
		}
		if name := hostSource(host, searchHosts); name != "" {
			return name, "Organic Search"
		}
		if name := hostSource(host, socialHosts); name != "" {
			return name, "Organic Social"
		}
		return host, "Referral"
	}
	if source != "" {
		return acquisition.UTMSource, "Referral"
	}
	return "Direct", "Direct"
}

func hostSource(host string, table map[string]string) string {
	for suffix, name := range table {
		if hostMatchesSuffix(host, suffix) {
			return name
		}
	}
	return ""
}

func hostMatchesSuffix(host, suffix string) bool {
	if host == suffix || strings.HasSuffix(host, "."+suffix) {
		return true
	}
	if !strings.HasSuffix(suffix, ".") {
		return false
	}
	return strings.HasPrefix(host, suffix) || strings.Contains(host, "."+suffix)
}

func named(source, fallback string) string {
	if source == "" {
		return fallback
	}
	return source
}

func first(query url.Values, key string) string {
	return strings.TrimSpace(query.Get(key))
}

func StripQuery(raw string) (pathname, pageURL string) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Path == "" && parsed.Host == "" {
		return raw, raw
	}
	pathname = parsed.Path
	if pathname == "" {
		pathname = "/"
	}
	keep := url.Values{}
	for _, key := range []string{"utm_source", "utm_medium", "utm_campaign", "utm_content", "utm_term", "ref", "source", "gclid", "fbclid", "msclkid", "ttclid", "li_fat_id", "twclid"} {
		if value := parsed.Query().Get(key); value != "" {
			keep.Set(key, value)
		}
	}
	parsed.RawQuery = keep.Encode()
	parsed.Fragment = ""
	return pathname, parsed.String()
}
