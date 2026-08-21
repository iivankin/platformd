package hosthub

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/iivankin/platformd/internal/hostconn"
)

func TestHostOTLPRelaysAuthenticatedRawProtobuf(t *testing.T) {
	payload := []byte{0x0a, 0x03, 0xff, 0x00, 0x7f}
	var received atomic.Int64
	collector := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		received.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/v1/traces" {
			t.Fatalf("collector request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Content-Type") != "application/x-protobuf" || request.Header.Get("Content-Encoding") != "gzip" {
			t.Fatalf("content headers = %q/%q", request.Header.Get("Content-Type"), request.Header.Get("Content-Encoding"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body, payload) {
			t.Fatalf("collector body = %x", body)
		}
		response.Header().Set("Content-Type", "application/x-protobuf")
		response.WriteHeader(http.StatusAccepted)
		_, _ = response.Write([]byte{0x08, 0x01})
	}))
	defer collector.Close()

	env := startHostMesh(t)
	env.hub.otlpBaseURL = collector.URL
	env.hub.otlpClient = collector.Client()
	child := env.joinChild(t, "edge-otlp", "203.0.113.60")

	request, err := http.NewRequest(
		http.MethodPost, env.server.URL+hostconn.OTLPPathPrefix+"/v1/traces", bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+child.hostToken)
	request.Header.Set("Content-Type", "application/x-protobuf")
	request.Header.Set("Content-Encoding", "gzip")
	response, err := env.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted || !bytes.Equal(body, []byte{0x08, 0x01}) || received.Load() != 1 {
		t.Fatalf("relay response/count = %d %x/%d", response.StatusCode, body, received.Load())
	}
}

func TestHostOTLPRejectsUnauthorizedAndOversizedRequests(t *testing.T) {
	var received atomic.Int64
	collector := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		received.Add(1)
	}))
	defer collector.Close()
	env := startHostMesh(t)
	env.hub.otlpBaseURL = collector.URL
	env.hub.otlpClient = collector.Client()
	endpoint := env.server.URL + hostconn.OTLPPathPrefix + "/v1/logs"

	unauthorized, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader([]byte{0x00}))
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Header.Set("Content-Type", "application/x-protobuf")
	response, err := env.client.Do(unauthorized)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", response.StatusCode)
	}

	child := env.joinChild(t, "edge-large", "203.0.113.61")
	oversized, err := http.NewRequest(
		http.MethodPost, endpoint, bytes.NewReader(make([]byte, hostconn.MaximumOTLPRequestBytes+1)),
	)
	if err != nil {
		t.Fatal(err)
	}
	oversized.Header.Set("Authorization", "Bearer "+child.hostToken)
	oversized.Header.Set("Content-Type", "application/x-protobuf")
	response, err = env.client.Do(oversized)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge || received.Load() != 0 {
		t.Fatalf("oversized status/collector requests = %d/%d", response.StatusCode, received.Load())
	}
}
