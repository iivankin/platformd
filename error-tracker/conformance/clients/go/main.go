package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/http"
)

func main() {
	testCase := os.Getenv("CONFORMANCE_CASE")
	if err := sentry.Init(sentry.ClientOptions{Dsn: os.Getenv("SENTRY_DSN")}); err != nil {
		panic(err)
	}
	sentry.ConfigureScope(func(scope *sentry.Scope) { scope.SetTag("conformance_case", testCase) })
	switch testCase {
	case "go":
		sentry.CaptureException(fmt.Errorf("go conformance"))
	case "net-http":
		integration := sentryhttp.New(sentryhttp.Options{Repanic: false, WaitForDelivery: true})
		server := httptest.NewServer(integration.Handle(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("net/http conformance")
		})))
		_, _ = http.Get(server.URL)
		server.Close()
	default:
		panic("unsupported conformance case")
	}
	if !sentry.Flush(10 * time.Second) {
		panic("Sentry SDK flush timed out")
	}
}
