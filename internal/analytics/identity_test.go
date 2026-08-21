package analytics

import (
	"net/http"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

func TestResolveIdentityDropsDeniedAndMakesGPCCookieless(t *testing.T) {
	t.Parallel()
	tracker := state.AnalyticsTracker{ID: "tracker", RootDomain: "shop.com"}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	denied := httptestRequest()
	denied.AddCookie(&http.Cookie{Name: ConsentCookie, Value: ConsentDenied})
	if identity := ResolveIdentity(tracker, denied, "1.1.1.1", "Mozilla", "install", now); !identity.Drop {
		t.Fatal("denied consent should drop")
	}

	gpc := httptestRequest()
	gpc.Header.Set("Sec-GPC", "1")
	gpc.AddCookie(&http.Cookie{Name: AidCookie, Value: "aid-1"})
	gpc.AddCookie(&http.Cookie{Name: SidCookie, Value: "sid-1"})
	identity := ResolveIdentity(tracker, gpc, "1.1.1.1", "Mozilla", "install", now)
	if identity.Drop || identity.Identified || identity.SessionID != "" ||
		identity.DistinctID != DailyHash(tracker.ID, "install", "1.1.1.1", "Mozilla", now) {
		t.Fatalf("GPC identity = %+v", identity)
	}
}

func TestResolveIdentityWithoutAIDHashesDaily(t *testing.T) {
	t.Parallel()
	tracker := state.AnalyticsTracker{ID: "tracker"}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	request := httptestRequest()
	identity := ResolveIdentity(tracker, request, "10.0.0.1", "Mozilla/5.0", "install", now)
	if identity.Drop || identity.Identified || identity.DistinctID == "" {
		t.Fatalf("cookieless identity = %+v", identity)
	}
	same := ResolveIdentity(tracker, request, "10.0.0.1", "Mozilla/5.0", "install", now)
	if same.DistinctID != identity.DistinctID {
		t.Fatal("same-day cookieless hash should be stable")
	}
	nextDay := ResolveIdentity(tracker, request, "10.0.0.1", "Mozilla/5.0", "install", now.Add(24*time.Hour))
	if nextDay.DistinctID == identity.DistinctID {
		t.Fatal("next-day cookieless hash should rotate")
	}
}

func TestResolveIdentityWithAIDIsIdentified(t *testing.T) {
	t.Parallel()
	tracker := state.AnalyticsTracker{ID: "tracker"}
	now := time.Now()
	request := httptestRequest()
	request.AddCookie(&http.Cookie{Name: AidCookie, Value: "aid-1"})
	request.AddCookie(&http.Cookie{Name: SidCookie, Value: "sid-1"})
	identity := ResolveIdentity(tracker, request, "1.1.1.1", "ua", "install", now)
	if identity.Drop || !identity.Identified || identity.DistinctID != "aid-1" || identity.SessionID != "sid-1" {
		t.Fatalf("identified identity = %+v", identity)
	}
}

func TestCookieDomainOmitsHostOnlyRoots(t *testing.T) {
	t.Parallel()
	if CookieDomain("shop.example") != ".shop.example" {
		t.Fatalf("cookie domain = %q", CookieDomain("shop.example"))
	}
	if CookieDomain("localhost") != "" || CookieDomain("127.0.0.1") != "" {
		t.Fatal("localhost and IPs must be host-only cookies")
	}
	if CookieDomain("shop.example:443") != ".shop.example" {
		t.Fatalf("ported cookie domain = %q", CookieDomain("shop.example:443"))
	}
}

func httptestRequest() *http.Request {
	request, _ := http.NewRequest(http.MethodPost, "/analytics/e", nil)
	return request
}
