package telemetry

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
	collectorl "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectort "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/ingress"
)

type browserConformanceBackend struct{}

func (browserConformanceBackend) ServiceBackend(string, int) (deployment.Backend, bool, error) {
	return deployment.Backend{}, false, nil
}

type browserOTLPRequest struct {
	body            []byte
	contentEncoding string
	contentType     string
	origin          string
	path            string
}

func browserWebVitalContext(payload *collectorl.ExportLogsServiceRequest) (string, bool, bool) {
	context := ""
	uncorrelated := false
	for _, resource := range payload.ResourceLogs {
		for _, scope := range resource.ScopeLogs {
			for _, record := range scope.LogRecords {
				if record.EventName != "browser.web_vital" {
					continue
				}
				for _, attribute := range record.Attributes {
					if attribute.Key == "browser.web_vital.name" && attribute.Value.GetStringValue() != "" {
						if len(record.TraceId) != 16 || len(record.SpanId) != 8 {
							uncorrelated = true
						} else if context == "" {
							context = string(record.TraceId) + string(record.SpanId)
						}
					}
				}
			}
		}
	}
	return context, context != "", uncorrelated
}

func browserLogSummaries(payload *collectorl.ExportLogsServiceRequest) []string {
	var summaries []string
	for _, resource := range payload.ResourceLogs {
		for _, scope := range resource.ScopeLogs {
			for _, record := range scope.LogRecords {
				name := ""
				for _, attribute := range record.Attributes {
					if attribute.Key == "browser.web_vital.name" {
						name = attribute.Value.GetStringValue()
					}
				}
				summaries = append(summaries, fmt.Sprintf(
					"%s/%s(trace=%d,span=%d)", record.EventName, name, len(record.TraceId), len(record.SpanId),
				))
			}
		}
	}
	return summaries
}

func TestBrowserOTLPIngressConformance(t *testing.T) {
	received := make(chan browserOTLPRequest, 64)
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
			received <- browserOTLPRequest{
				body: body, contentEncoding: request.Header.Get("Content-Encoding"),
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
		"otel.example.com": {OTLPPathPrefix: "/browser"},
	})

	var browserBundle []byte
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		host := request.Host
		if parsed, _, splitErr := net.SplitHostPort(host); splitErr == nil {
			host = parsed
		}
		if host == "app.example.com" {
			if request.URL.Path == "/telemetry.js" {
				response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
				_, _ = response.Write(browserBundle)
				return
			}
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(response, `<!doctype html><html><body><main>Browser telemetry conformance</main><script type="module" src="/telemetry.js"></script></body></html>`)
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
	collectorURL := fmt.Sprintf("https://otel.example.com:%d/browser", port)
	frontendDirectory := filepath.Join("..", "..", "_frontend")
	bundlePath := filepath.Join(t.TempDir(), "telemetry.js")
	build := exec.Command("bun", "run", "conformance/browser-otel.ts", collectorURL, bundlePath)
	build.Dir = frontendDirectory
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build generated browser telemetry setup: %v\n%s", buildErr, output)
	}
	browserBundle, err = os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(browserBundle) == 0 {
		t.Fatal("generated browser telemetry bundle is empty")
	}

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
		"document.body.dataset.telemetryStatus === 'exported' || Boolean(document.body.dataset.telemetryError)", nil,
		playwright.PageWaitForFunctionOptions{Timeout: playwright.Float(15_000)},
	); err != nil {
		t.Fatal(err)
	}
	body := page.Locator("body")
	status, err := body.GetAttribute("data-telemetry-status")
	if err != nil {
		t.Fatal(err)
	}
	if status != "exported" {
		failure, _ := body.GetAttribute("data-telemetry-error")
		t.Fatalf("browser fetch status = %q, error = %q", status, failure)
	}

	paths := make(map[string]bool)
	spanContexts := make(map[string]bool)
	webVitalContext := ""
	uncorrelatedWebVital := false
	var logSummaries []string
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for !(paths["/v1/traces"] && paths["/v1/logs"] && webVitalContext != "") {
		select {
		case request := <-received:
			reader, err := gzip.NewReader(bytes.NewReader(request.body))
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
			if (request.path != "/v1/traces" && request.path != "/v1/logs") || request.contentType != "application/x-protobuf" ||
				request.contentEncoding != "gzip" || request.origin != fmt.Sprintf("https://app.example.com:%d", port) ||
				len(body) == 0 {
				t.Fatalf("browser OTLP request = %+v", request)
			}
			if request.path == "/v1/traces" {
				var payload collectort.ExportTraceServiceRequest
				if err := proto.Unmarshal(body, &payload); err != nil || len(payload.ResourceSpans) == 0 {
					t.Fatalf("decode browser trace payload: %v", err)
				}
				for _, resource := range payload.ResourceSpans {
					for _, scope := range resource.ScopeSpans {
						for _, span := range scope.Spans {
							spanContexts[string(span.TraceId)+string(span.SpanId)] = true
						}
					}
				}
			} else {
				var payload collectorl.ExportLogsServiceRequest
				if err := proto.Unmarshal(body, &payload); err != nil || len(payload.ResourceLogs) == 0 {
					t.Fatalf("decode browser log payload: %v", err)
				}
				logSummaries = append(logSummaries, browserLogSummaries(&payload)...)
				context, ok, uncorrelated := browserWebVitalContext(&payload)
				uncorrelatedWebVital = uncorrelatedWebVital || uncorrelated
				if ok {
					webVitalContext = context
				}
			}
			paths[request.path] = true
		case <-deadline.C:
			t.Fatalf(
				"browser OTLP conformance incomplete: paths=%v web_vital=%t exported_span_contexts=%d logs=%v",
				paths, webVitalContext != "", len(spanContexts), logSummaries,
			)
		}
	}
	if !paths["/v1/traces"] || !paths["/v1/logs"] {
		t.Fatalf("browser OTLP paths = %v", paths)
	}
	if !spanContexts[webVitalContext] {
		t.Fatal("browser Web Vital is not correlated with an exported document-load span")
	}
	if uncorrelatedWebVital {
		t.Fatal("browser exported an uncorrelated Web Vital")
	}
}

func TestBrowserOTLPIngressRejectsUnconfiguredPath(t *testing.T) {
	router, err := ingress.New(ingress.Config{
		AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(), Backends: browserConformanceBackend{},
	})
	if err != nil {
		t.Fatal(err)
	}
	router.ReloadServiceTelemetry(map[string]ingress.ServiceTelemetryRoute{
		"otel.example.com": {OTLPPathPrefix: "/browser"},
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
