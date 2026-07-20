package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/portforward"
)

type portForwardResourceStub struct{}

func (portForwardResourceStub) Resource(context.Context, string, string, string) error { return nil }
func (portForwardResourceStub) ResolveResourceAddress(string, string, string, int) (string, error) {
	return "10.42.0.5:5432", nil
}
func (portForwardResourceStub) RecordPortForwardTicket(context.Context, portforward.AuditRecord) error {
	return nil
}

func TestMCPPortForwardReturnsCLIInstructions(t *testing.T) {
	handler := newTestHandler(t, &repositoryStub{})
	application, err := portforward.New(portforward.Config{
		Repository: portForwardResourceStub{}, Resolver: portForwardResourceStub{}, Audit: portForwardResourceStub{},
		NewID: func() (string, error) { return "ticket-id", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler.portForwards = application

	list := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`), automation.Identity{TokenID: "admin", Role: "admin"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if !strings.Contains(response.Body.String(), `"name":"create_port_forward"`) {
		t.Fatalf("port forward tool is missing: %s", response.Body)
	}

	call := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_port_forward","arguments":{"projectId":"project","resourceKind":"postgres","resourceId":"database","port":5432,"localPort":15432}}}`), automation.Identity{TokenID: "admin", Role: "admin"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	body := response.Body.String()
	for _, expected := range []string{"ticket-id", "platformd-forward", "raw.githubusercontent.com/iivankin/platformd/main/install.sh", "sh -s -- forward", "wss://api.example.com/api/v1/port-forward", "--local-port 15432"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("MCP result does not contain %q: %s", expected, body)
		}
	}
}
