package daemon

import (
	"context"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

type objectStoreProxyLookupStub struct{}

func (objectStoreProxyLookupStub) ObjectStoreByHostname(context.Context, string) (state.ObjectStore, error) {
	return state.ObjectStore{}, nil
}

func TestObjectStoreProxyChangesOnlyTransportDestination(t *testing.T) {
	handler, err := newObjectStoreProxy(objectStoreProxyLookupStub{}, &runtimeStack{})
	if err != nil {
		t.Fatal(err)
	}
	incoming := httptest.NewRequest("PUT", "https://objects.example.com/sdk-bucket/a%2Fb?x-id=PutObject&partNumber=1", nil)
	incoming.Host = "objects.example.com"
	incoming.Header.Set(objectStorePublicStoreHeader, "spoofed-store")
	target := &url.URL{Scheme: "http", Host: "10.88.0.1:9000"}
	route := objectStoreProxyRoute{target: target, storeID: "selected-store"}
	incoming = incoming.WithContext(context.WithValue(incoming.Context(), objectStoreProxyRouteKey{}, route))
	outgoing := incoming.Clone(incoming.Context())

	handler.proxy.Rewrite(&httputil.ProxyRequest{In: incoming, Out: outgoing})

	if outgoing.URL.Scheme != target.Scheme || outgoing.URL.Host != target.Host {
		t.Fatalf("transport target = %s://%s", outgoing.URL.Scheme, outgoing.URL.Host)
	}
	if outgoing.Host != incoming.Host || outgoing.URL.EscapedPath() != incoming.URL.EscapedPath() ||
		outgoing.URL.RawQuery != incoming.URL.RawQuery {
		t.Fatalf("signed request changed: host=%q path=%q query=%q", outgoing.Host, outgoing.URL.EscapedPath(), outgoing.URL.RawQuery)
	}
	if got := outgoing.Header.Get(objectStorePublicStoreHeader); got != route.storeID {
		t.Fatalf("public store boundary = %q", got)
	}
}
