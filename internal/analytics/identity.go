package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

const (
	AidCookie     = "platformd_aid"
	SidCookie     = "platformd_sid"
	ConsentCookie = "platformd_consent"
	ConsentDenied = "denied"
	TrackerHeader = "X-Platformd-Tracker-Id"
)

type Identity struct {
	DistinctID string
	SessionID  string
	Identified bool
	Drop       bool
}

func RequestDenied(request *http.Request) bool {
	cookie, _ := request.Cookie(ConsentCookie)
	return cookie != nil && cookie.Value == ConsentDenied
}

func RequestGPC(request *http.Request) bool {
	return strings.TrimSpace(request.Header.Get("Sec-GPC")) == "1"
}

func ResolveIdentity(tracker state.AnalyticsTracker, request *http.Request, clientIP, userAgent, installationID string, now time.Time) Identity {
	if RequestDenied(request) {
		return Identity{Drop: true}
	}
	if aid, _ := request.Cookie(AidCookie); !RequestGPC(request) && aid != nil && strings.TrimSpace(aid.Value) != "" {
		return cookieIdentity(request)
	}
	return Identity{DistinctID: DailyHash(tracker.ID, installationID, clientIP, userAgent, now)}
}

func cookieIdentity(request *http.Request) Identity {
	aid, _ := request.Cookie(AidCookie)
	if aid == nil || strings.TrimSpace(aid.Value) == "" {
		return Identity{Drop: true}
	}
	identity := Identity{DistinctID: aid.Value, Identified: true}
	if sid, _ := request.Cookie(SidCookie); sid != nil {
		identity.SessionID = sid.Value
	}
	return identity
}

func DailyHash(trackerID, installationID, ip, userAgent string, now time.Time) string {
	day := now.UTC().Format("2006-01-02")
	sum := sha256.Sum256([]byte(strings.Join([]string{ip, userAgent, trackerID, installationID, day}, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func ClientIP(request *http.Request) string {
	if forwarded := strings.TrimSpace(request.Header.Get("CF-Connecting-IP")); forwarded != "" {
		return forwarded
	}
	if forwarded := strings.TrimSpace(request.Header.Get("X-Forwarded-For")); forwarded != "" {
		if comma := strings.IndexByte(forwarded, ','); comma >= 0 {
			return strings.TrimSpace(forwarded[:comma])
		}
		return forwarded
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}

func CookieDomain(root string) string {
	root = strings.TrimPrefix(state.NormalizeTrackerRoot(root), ".")
	if root == "" || !strings.Contains(root, ".") || net.ParseIP(root) != nil {
		return ""
	}
	return "." + root
}

func MatchTracker(trackers []state.AnalyticsTracker, host string) (state.AnalyticsTracker, bool) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	var matched state.AnalyticsTracker
	found := false
	for _, tracker := range trackers {
		if !state.HostMatchesTracker(host, tracker.RootDomain) {
			continue
		}
		if !found || len(tracker.RootDomain) > len(matched.RootDomain) {
			matched = tracker
			found = true
		}
	}
	return matched, found
}
