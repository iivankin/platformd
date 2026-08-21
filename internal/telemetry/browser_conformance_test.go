package telemetry

import (
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/ingress"
)

type browserConformanceBackend struct{}

func (browserConformanceBackend) ServiceBackend(string, int) (deployment.Backend, bool, error) {
	return deployment.Backend{}, false, nil
}

type browserTraceRequest struct {
	body            string
	contentEncoding string
	contentType     string
	origin          string
	path            string
}

func TestBrowserOTLPTraceIngressConformance(t *testing.T) {
	received := make(chan browserTraceRequest, 1)
	router, err := ingress.New(ingress.Config{
		AdminHostname: "admin.example.com",
		AdminHandler:  http.NotFoundHandler(),
		Backends:      browserConformanceBackend{},
		ServiceTelemetryHandler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				http.Error(response, "read trace payload", http.StatusBadRequest)
				return
			}
			received <- browserTraceRequest{
				body: string(body), contentEncoding: request.Header.Get("Content-Encoding"),
				contentType: request.Header.Get("Content-Type"), origin: request.Header.Get("Origin"),
				path: request.URL.Path,
			}
			response.WriteHeader(http.StatusAccepted)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	router.ReloadServiceTelemetry(map[string]ingress.ServiceTelemetryRoute{
		"otel.example.com": {OTLPTracePath: "/browser/v1/traces"},
	})

	var collectorURL string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		host := request.Host
		if parsed, _, splitErr := net.SplitHostPort(host); splitErr == nil {
			host = parsed
		}
		if host == "app.example.com" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(response, `<!doctype html><html><body><script>
(async function () {
  const payload = new Uint8Array([10, 0]);
  const body = await new Response(
    new Blob([payload]).stream().pipeThrough(new CompressionStream('gzip'))
  ).arrayBuffer();
  const result = await fetch(%q, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-protobuf',
      'Content-Encoding': 'gzip'
    },
    body
  });
  document.body.dataset.status = String(result.status);
})().catch(function (error) {
  document.body.dataset.error = String(error);
});
</script></body></html>`, collectorURL)
			return
		}
		// Both test hostnames resolve to loopback. Chromium's local-network
		// preflight is specific to that test transport and is not involved when
		// the configured collector is served through its public DNS address.
		if request.Header.Get("Access-Control-Request-Private-Network") == "true" {
			response.Header().Set("Access-Control-Allow-Private-Network", "true")
		}
		// The production ingress receives HTTPS on port 443, while httptest uses a
		// random port that is intentionally rejected by NormalizeHostHeader.
		forwarded := request.Clone(request.Context())
		forwarded.Host = host
		router.ServeHTTP(response, forwarded)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	port := server.Listener.Addr().(*net.TCPAddr).Port
	collectorURL = fmt.Sprintf("https://otel.example.com:%d/browser/v1/traces", port)

	options := &playwright.RunOptions{Browsers: []string{"chromium"}, Verbose: false}
	pw, err := playwright.Run(options)
	if err != nil {
		if installErr := playwright.Install(options); installErr != nil {
			t.Fatalf("playwright install chromium: %v (start: %v)", installErr, err)
		}
		pw, err = playwright.Run(options)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pw.Stop() })
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Args: []string{"--host-resolver-rules=MAP app.example.com 127.0.0.1,MAP otel.example.com 127.0.0.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	context, err := browser.NewContext(playwright.BrowserNewContextOptions{IgnoreHttpsErrors: playwright.Bool(true)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = context.Close() })
	page, err := context.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := page.Goto(fmt.Sprintf("https://app.example.com:%d/", port)); err != nil {
		t.Fatal(err)
	}
	if _, err := page.WaitForFunction(
		"document.body.dataset.status === '202' || Boolean(document.body.dataset.error)", nil,
		playwright.PageWaitForFunctionOptions{Timeout: playwright.Float(8_000)},
	); err != nil {
		t.Fatal(err)
	}
	body := page.Locator("body")
	status, err := body.GetAttribute("data-status")
	if err != nil {
		t.Fatal(err)
	}
	if status != "202" {
		failure, _ := body.GetAttribute("data-error")
		t.Fatalf("browser fetch status = %q, error = %q", status, failure)
	}

	select {
	case request := <-received:
		reader, err := gzip.NewReader(strings.NewReader(request.body))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if request.path != "/v1/traces" || request.contentType != "application/x-protobuf" ||
			request.contentEncoding != "gzip" || request.origin != fmt.Sprintf("https://app.example.com:%d", port) ||
			string(body) != string([]byte{10, 0}) {
			t.Fatalf("browser OTLP request = %+v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("browser OTLP trace request was not forwarded")
	}
}

func TestBrowserOTLPTraceIngressRejectsUnconfiguredPath(t *testing.T) {
	router, err := ingress.New(ingress.Config{
		AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(), Backends: browserConformanceBackend{},
	})
	if err != nil {
		t.Fatal(err)
	}
	router.ReloadServiceTelemetry(map[string]ingress.ServiceTelemetryRoute{
		"otel.example.com": {OTLPTracePath: "/browser/v1/traces"},
	})
	request := httptest.NewRequest(http.MethodPost, "https://otel.example.com/browser/v1/metrics", strings.NewReader("payload"))
	request.Host = "otel.example.com"
	request.TLS.ServerName = "otel.example.com"
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unconfigured browser telemetry path status = %d", response.Code)
	}
}
