package managedimages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogListsOnlyValidatedOfficialTags(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v2/namespaces/library/repositories/postgres/tags" || request.URL.Query().Get("page") != "2" || request.URL.Query().Get("page_size") != "2" || request.URL.Query().Get("name") != "18.3" {
			t.Fatalf("request URL = %s", request.URL.String())
		}
		response.Header().Set("X-RateLimit-Remaining", "17")
		response.Header().Set("X-RateLimit-Reset", "123")
		_, _ = response.Write([]byte(`{
  "count":3,
  "next":"https://attacker.example/ignored",
  "previous":"https://attacker.example/ignored",
  "results":[
    {"name":"18.3","last_updated":"2026-06-01T00:00:00Z","images":[{"architecture":"amd64","os":"linux","digest":"sha256:abc","size":42}]},
    {"name":"invalid tag","last_updated":"2026-06-01T00:00:00Z","images":[]}
  ]
}`))
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.List(context.Background(), PostgreSQL, 2, 2, " 18.3 ")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tags) != 1 || page.Tags[0].Name != "18.3" || len(page.Tags[0].Platforms) != 1 || page.NextPage != 3 || page.PreviousPage != 1 || page.RateLimitRemaining != 17 || page.RateLimitReset != 123 {
		t.Fatalf("page = %+v", page)
	}
}

func TestCatalogHidesLatestTag(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("name") != "" {
			t.Fatalf("unexpected search query = %q", request.URL.Query().Get("name"))
		}
		_, _ = response.Write([]byte(`{
  "count":2,
  "next":null,
  "previous":null,
  "results":[
    {"name":"latest","last_updated":"2026-06-02T00:00:00Z","images":[]},
    {"name":"18.4-alpine3.23","last_updated":"2026-06-01T00:00:00Z","images":[]}
  ]
}`))
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.List(context.Background(), PostgreSQL, 1, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Tags) != 1 || page.Tags[0].Name != "18.4-alpine3.23" {
		t.Fatalf("page = %+v", page)
	}
}

func TestCatalogRejectsUnboundedOrInvalidInputs(t *testing.T) {
	client, err := New("https://hub.example", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		engine Engine
		page   int
		size   int
	}{{"mysql", 1, 10}, {PostgreSQL, -1, 10}, {Redis, 1, 101}} {
		if _, err := client.List(context.Background(), input.engine, input.page, input.size, ""); err == nil {
			t.Fatalf("input %+v was accepted", input)
		}
	}
	if _, err := Reference(PostgreSQL, "invalid tag"); err == nil {
		t.Fatal("invalid manual tag was accepted")
	}
	if reference, err := Reference(Redis, "7.4-alpine"); err != nil || reference != "docker.io/library/redis:7.4-alpine" {
		t.Fatalf("reference = %q, %v", reference, err)
	}
	if _, err := client.List(context.Background(), PostgreSQL, 1, 10, strings.Repeat("x", 129)); err == nil {
		t.Fatal("oversized search was accepted")
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusBadGateway)
		_, _ = response.Write([]byte(strings.Repeat("x", maximumResponseBytes+1)))
	}))
	defer server.Close()
	bounded, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bounded.List(context.Background(), Redis, 1, 10, ""); err == nil || !strings.Contains(err.Error(), "exceeds 2 MiB") {
		t.Fatalf("oversized response error = %v", err)
	}
}
