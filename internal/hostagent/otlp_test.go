package hostagent

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/iivankin/platformd/internal/hostconn"
)

func TestOTLPForwarderRelaysRawProtobuf(t *testing.T) {
	payload := []byte{0x0a, 0x03, 0xff, 0x00, 0x7f}
	parent := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != hostconn.OTLPPathPrefix+"/v1/logs" {
			t.Fatalf("parent request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer host-token" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Content-Type") != "application/x-protobuf" || request.Header.Get("Content-Encoding") != "gzip" {
			t.Fatalf("content headers = %q/%q", request.Header.Get("Content-Type"), request.Header.Get("Content-Encoding"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body, payload) {
			t.Fatalf("body = %x", body)
		}
		response.Header().Set("Content-Type", "application/x-protobuf")
		response.WriteHeader(http.StatusAccepted)
		_, _ = response.Write([]byte{0x08, 0x01})
	}))
	defer parent.Close()

	forwarder, err := NewForwarderWithOptions(ForwarderOptions{
		ParentURL:  parent.URL,
		HostToken:  "host-token",
		HTTPClient: parent.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/x-protobuf")
	request.Header.Set("Content-Encoding", "gzip")
	response := httptest.NewRecorder()
	forwarder.ingest(response, request)

	if response.Code != http.StatusAccepted || !bytes.Equal(response.Body.Bytes(), []byte{0x08, 0x01}) {
		t.Fatalf("response = %d %x", response.Code, response.Body.Bytes())
	}
}

func TestOTLPForwarderRejectsKnownOversizedBody(t *testing.T) {
	var requests atomic.Int64
	parent := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer parent.Close()
	forwarder, err := NewForwarderWithOptions(ForwarderOptions{
		ParentURL:  parent.URL,
		HostToken:  "host-token",
		HTTPClient: parent.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost, "/v1/logs", bytes.NewReader(make([]byte, hostconn.MaximumOTLPRequestBytes+1)),
	)
	request.Header.Set("Content-Type", "application/x-protobuf")
	response := httptest.NewRecorder()
	forwarder.ingest(response, request)

	if response.Code != http.StatusRequestEntityTooLarge || requests.Load() != 0 {
		t.Fatalf("oversized response/parent requests = %d/%d", response.Code, requests.Load())
	}
}
