package analytics

import (
	"net/http"
	"strings"
)

type BotClass struct {
	Kind string
	Name string
}

var trainingBots = []string{
	"GPTBot", "ClaudeBot", "Google-Extended", "Bytespider", "CCBot",
	"anthropic-ai", "Applebot-Extended",
}
var searchBots = []string{
	"OAI-SearchBot", "Claude-SearchBot", "PerplexityBot",
}
var fetchBots = []string{
	"ChatGPT-User", "Claude-User", "Perplexity-User",
}

func ClassifyBot(userAgent string) BotClass {
	if kind, name := matchBot(userAgent, "training", trainingBots); name != "" {
		return BotClass{Kind: kind, Name: name}
	}
	if kind, name := matchBot(userAgent, "search", searchBots); name != "" {
		return BotClass{Kind: kind, Name: name}
	}
	if kind, name := matchBot(userAgent, "fetch", fetchBots); name != "" {
		return BotClass{Kind: kind, Name: name}
	}
	return BotClass{}
}

func matchBot(userAgent, kind string, names []string) (string, string) {
	for _, name := range names {
		if strings.Contains(userAgent, name) {
			return kind, name
		}
	}
	return "", ""
}

func IsDocumentRequest(request *http.Request) bool {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return false
	}
	accept := request.Header.Get("Accept")
	return accept == "" || strings.Contains(accept, "text/html") || strings.Contains(accept, "*/*")
}

type Device struct {
	Browser        string
	BrowserVersion string
	OS             string
	OSVersion      string
	Device         string
}

func ParseUserAgent(userAgent string) Device {
	device := Device{Browser: "Other", OS: "Other", Device: "Desktop"}
	switch {
	case contains(userAgent, "iPhone"):
		device.Device, device.OS = "Mobile", "iOS"
	case contains(userAgent, "iPad"):
		device.Device, device.OS = "Tablet", "iOS"
	case contains(userAgent, "Android"):
		device.OS = "Android"
		if contains(userAgent, "Mobile") {
			device.Device = "Mobile"
		} else {
			device.Device = "Tablet"
		}
	case contains(userAgent, "Mac OS X"):
		device.OS = "macOS"
	case contains(userAgent, "Windows"):
		device.OS = "Windows"
	case contains(userAgent, "Linux"):
		device.OS = "Linux"
	}
	switch device.OS {
	case "iOS":
		device.OSVersion = strings.ReplaceAll(after(userAgent, "OS "), "_", ".")
	case "Android":
		device.OSVersion = after(userAgent, "Android ")
	case "macOS":
		device.OSVersion = strings.ReplaceAll(after(userAgent, "Mac OS X "), "_", ".")
	case "Windows":
		device.OSVersion = after(userAgent, "Windows NT ")
	}
	switch {
	case contains(userAgent, "Edg/"):
		device.Browser, device.BrowserVersion = "Edge", after(userAgent, "Edg/")
	case contains(userAgent, "Chrome/"):
		device.Browser, device.BrowserVersion = "Chrome", after(userAgent, "Chrome/")
	case contains(userAgent, "Firefox/"):
		device.Browser, device.BrowserVersion = "Firefox", after(userAgent, "Firefox/")
	case contains(userAgent, "Safari/") && contains(userAgent, "Version/"):
		device.Browser, device.BrowserVersion = "Safari", after(userAgent, "Version/")
	}
	return device
}

func contains(value, token string) bool {
	return strings.Contains(value, token)
}

func after(value, token string) string {
	index := strings.Index(value, token)
	if index < 0 {
		return ""
	}
	rest := value[index+len(token):]
	if slash := strings.IndexAny(rest, " ;)"); slash >= 0 {
		rest = rest[:slash]
	}
	if dot := strings.IndexByte(rest, '.'); dot > 0 {
		return rest[:dot]
	}
	return rest
}
