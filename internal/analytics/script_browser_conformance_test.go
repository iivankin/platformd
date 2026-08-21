package analytics

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

func TestAnalyticsSDKBrowserConformance(t *testing.T) {
	browser := launchPlaywrightChromium(t)
	sdkSource := analyticsSDKSource(t)

	t.Run("opt-out sdk cookies heatmap spa", func(t *testing.T) {
		origin, captured := startShopTLS(t, "opt-out", sdkSource)
		page := newShopPage(t, browser, false)
		encodedRequest := make(chan string, 1)
		page.OnRequest(func(request playwright.Request) {
			if !strings.HasSuffix(request.URL(), "/analytics/e") {
				return
			}
			if encoding, err := request.HeaderValue("content-encoding"); err == nil && encoding != "" {
				select {
				case encodedRequest <- encoding:
				default:
				}
			}
		})
		if _, err := page.Goto(origin + "/pricing?utm_source=google"); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator("#cta").WaitFor(); err != nil {
			t.Fatal(err)
		}
		cookies := evalString(t, page, "document.cookie")
		if !strings.Contains(cookies, AidCookie+"=") || !strings.Contains(cookies, SidCookie+"=") {
			t.Fatalf("SDK cookies = %q", cookies)
		}
		events := captured.waitNamed(t, "$pageview", 1)
		pageview := namedEvents(events, "$pageview")[0]
		if pageview.Pathname != "/pricing" || pageview.UTMSource != "google" || pageview.Interactive != 1 {
			t.Fatalf("pageview = %+v", pageview)
		}
		if !uuidPattern.MatchString(pageview.DistinctID) || !uuidPattern.MatchString(pageview.SessionID) {
			t.Fatalf("pageview identity = %+v", pageview)
		}
		if kind := evalString(t, page, "typeof window.platformd"); kind != "undefined" {
			t.Fatalf("legacy window.platformd = %q", kind)
		}

		evalString(t, page, "analytics.track('checkout_completed',{orderId:'order-1',revenue:49.99,currency:'USD'}), 'ok'")
		checkout := namedEvents(captured.waitNamed(t, "checkout_completed", 1), "checkout_completed")[0]
		if checkout.Revenue == nil || *checkout.Revenue != 49.99 || checkout.Currency != "USD" || propValue(checkout, "orderId") != "order-1" {
			t.Fatalf("checkout event = %+v keys=%v values=%v", checkout, checkout.PropsKeys, checkout.PropsValues)
		}
		select {
		case encoding := <-encodedRequest:
			t.Fatalf("analytics SDK sent content-encoding %q", encoding)
		default:
		}

		if err := page.Locator("#cta").Click(); err != nil {
			t.Fatal(err)
		}
		heatmap := namedEvents(captured.waitNamed(t, "$heatmap", 1), "$heatmap")[0]
		if propValue(heatmap, "event_type") != "click" || propValue(heatmap, "x") == "" {
			t.Fatalf("heatmap = %+v keys=%v values=%v", heatmap, heatmap.PropsKeys, heatmap.PropsValues)
		}

		evalString(t, page, "analytics.openFeatureHook().after({flagKey:'pricing-v2'}, {variant:'true'}), 'ok'")
		flag := namedEvents(captured.waitNamed(t, "$flag_called", 1), "$flag_called")[0]
		if propValue(flag, "flag") != "pricing-v2" || propValue(flag, "variant") != "true" {
			t.Fatalf("flag = %+v keys=%v values=%v", flag, flag.PropsKeys, flag.PropsValues)
		}

		evalString(t, page, "history.pushState({}, '', '/account'), 'ok'")
		pages := namedEvents(captured.waitNamed(t, "$pageview", 2), "$pageview")
		if pages[1].Pathname != "/account" {
			t.Fatalf("spa pageview = %+v", pages[1])
		}
		evalString(t, page, "history.replaceState({}, '', '/billing'), 'ok'")
		pages = namedEvents(captured.waitNamed(t, "$pageview", 3), "$pageview")
		if pages[2].Pathname != "/billing" {
			t.Fatalf("replaceState pageview = %+v", pages[2])
		}
	})

	t.Run("opt-in waits for consent", func(t *testing.T) {
		origin, captured := startShopTLS(t, "opt-in", sdkSource)
		page := newShopPage(t, browser, false)
		if _, err := page.Goto(origin + "/pricing"); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator("#cta").WaitFor(); err != nil {
			t.Fatal(err)
		}
		if kind := evalString(t, page, "typeof window.analytics"); kind != "object" {
			t.Fatalf("analytics = %q", kind)
		}
		time.Sleep(400 * time.Millisecond)
		if captured.len() != 0 {
			t.Fatalf("opt-in sent before consent: %+v", captured.snapshot())
		}
		evalString(t, page, "analytics.consent('granted'), 'ok'")
		pageview := namedEvents(captured.waitNamed(t, "$pageview", 1), "$pageview")[0]
		if pageview.Pathname != "/pricing" || !uuidPattern.MatchString(pageview.DistinctID) {
			t.Fatalf("granted pageview = %+v", pageview)
		}
	})

	t.Run("cookieless hashes identity", func(t *testing.T) {
		origin, captured := startShopTLS(t, "cookieless", sdkSource)
		page := newShopPage(t, browser, false)
		if _, err := page.Goto(origin + "/pricing"); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator("#cta").WaitFor(); err != nil {
			t.Fatal(err)
		}
		cookies := evalString(t, page, "document.cookie")
		if strings.Contains(cookies, AidCookie+"=") || strings.Contains(cookies, SidCookie+"=") {
			t.Fatalf("cookieless cookies = %q", cookies)
		}
		pageview := namedEvents(captured.waitNamed(t, "$pageview", 1), "$pageview")[0]
		if !cookielessID.MatchString(pageview.DistinctID) || pageview.SessionID != "" {
			t.Fatalf("cookieless identity = %+v", pageview)
		}
	})

	t.Run("gpc sends cookieless", func(t *testing.T) {
		origin, captured := startShopTLS(t, "opt-out", sdkSource)
		page := newShopPage(t, browser, true)
		if _, err := page.Goto(origin + "/pricing"); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator("#cta").WaitFor(); err != nil {
			t.Fatal(err)
		}
		if kind := evalString(t, page, "typeof window.analytics"); kind != "object" {
			t.Fatalf("analytics = %q", kind)
		}
		if !evalBool(t, page, "navigator.globalPrivacyControl === true") {
			t.Fatal("GPC init script did not stick")
		}
		pageview := namedEvents(captured.waitNamed(t, "$pageview", 1), "$pageview")[0]
		if !cookielessID.MatchString(pageview.DistinctID) || pageview.SessionID != "" {
			t.Fatalf("GPC identity = %+v", pageview)
		}
		cookies := evalString(t, page, "document.cookie")
		if strings.Contains(cookies, AidCookie+"=") || strings.Contains(cookies, SidCookie+"=") {
			t.Fatalf("GPC cookies = %q", cookies)
		}
	})
}

func analyticsSDKSource(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve analytics conformance test path")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	path := filepath.Join(repositoryRoot, "dist", "npm", "analytics", "index.js")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read built @platformd/analytics SDK (run make frontend first): %v", err)
	}
	return string(source)
}

func startShopTLS(t *testing.T, mode, sdkSource string) (string, *ingestLog) {
	t.Helper()
	target, captured, client := ingestCapture(t)
	catalog := shopCatalog()
	handler := NewHandler(catalog, client, target, nil)
	server := httptest.NewUnstartedServer(shopSite(handler, mode, sdkSource))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	port := server.Listener.Addr().(*net.TCPAddr).Port
	return fmt.Sprintf("https://shop.example:%d", port), captured
}

func shopSite(analytics http.Handler, mode, sdkSource string) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if Reserved(request.Method, request.URL.Path) {
			analytics.ServeHTTP(response, request)
			return
		}
		if request.Method == http.MethodGet && request.URL.Path == "/platformd-analytics.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = io.WriteString(response, sdkSource)
			return
		}
		title := "Pricing"
		if strings.HasPrefix(request.URL.Path, "/account") {
			title = "Account"
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(response, `<!doctype html>
<html>
<head><meta charset="utf-8"><title>%s</title></head>
<body>
<main>
<h1>%s</h1>
<button id="cta" type="button">Buy</button>
<div id="spacer" style="height:2400px"></div>
</main>
<script type="module">
import { createAnalytics } from "/platformd-analytics.js";
window.analytics = createAnalytics({ mode: %q, cookieDomain: ".shop.example" });
</script>
</body>
</html>`, title, title, mode)
	})
}

func launchPlaywrightChromium(t *testing.T) playwright.Browser {
	t.Helper()
	opts := &playwright.RunOptions{Browsers: []string{"chromium"}, Verbose: false}
	browser, pw, err := startPlaywrightChromium(opts)
	if err != nil {
		if installErr := playwright.Install(&playwright.RunOptions{Browsers: []string{"chromium"}}); installErr != nil {
			t.Fatalf("playwright install chromium: %v (start: %v)", installErr, err)
		}
		browser, pw, err = startPlaywrightChromium(opts)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = browser.Close()
		_ = pw.Stop()
	})
	return browser
}

func startPlaywrightChromium(opts *playwright.RunOptions) (playwright.Browser, *playwright.Playwright, error) {
	pw, err := playwright.Run(opts)
	if err != nil {
		return nil, nil, err
	}
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Args: []string{"--host-resolver-rules=MAP shop.example 127.0.0.1"},
	})
	if err != nil {
		_ = pw.Stop()
		return nil, nil, err
	}
	return browser, pw, nil
}

func newShopPage(t *testing.T, browser playwright.Browser, gpc bool) playwright.Page {
	t.Helper()
	context, err := browser.NewContext(playwright.BrowserNewContextOptions{
		IgnoreHttpsErrors: playwright.Bool(true),
		Viewport:          &playwright.Size{Width: 1440, Height: 900},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = context.Close() })
	if gpc {
		if err := context.AddInitScript(playwright.Script{
			Content: playwright.String(`Object.defineProperty(Navigator.prototype, 'globalPrivacyControl', {get: function(){ return true; }});`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := context.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func evalString(t *testing.T, page playwright.Page, expression string) string {
	t.Helper()
	value, err := page.Evaluate(expression)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := value.(string)
	return text
}

func evalBool(t *testing.T, page playwright.Page, expression string) bool {
	t.Helper()
	value, err := page.Evaluate(expression)
	if err != nil {
		t.Fatal(err)
	}
	flag, _ := value.(bool)
	return flag
}

var (
	uuidPattern  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	cookielessID = regexp.MustCompile(`^[0-9a-f]{32}$`)
)
