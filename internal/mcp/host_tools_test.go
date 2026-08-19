package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/state"
)

type hostHubStub struct {
	hosts []state.Host
}

func (stub *hostHubStub) Hosts(context.Context) ([]state.Host, error) {
	return stub.hosts, nil
}

func (*hostHubStub) Connected(string) bool { return true }

func (*hostHubStub) ActiveHostJoinTokens(context.Context) ([]state.HostJoinToken, error) {
	return nil, nil
}

func (*hostHubStub) CreateJoinToken(context.Context, string, string, string, string) (string, state.HostJoinToken, error) {
	return "pjn_test", state.HostJoinToken{ID: "token", Name: "edge-1"}, nil
}

func (*hostHubStub) JoinCommand(token string) string {
	return "sudo platformd join --url https://admin.example.com --token " + token
}

func (*hostHubStub) DeleteHost(context.Context, state.DeleteHostInput) error { return nil }

func (*hostHubStub) DeleteJoinToken(context.Context, state.DeleteHostJoinTokenInput) error {
	return nil
}

func TestMCPHostToolsAreAdminScoped(t *testing.T) {
	handler := newTestHandler(t, &repositoryStub{})
	handler.hosts = &hostHubStub{hosts: []state.Host{{ID: "host-1", Name: "edge-1"}}}

	list := mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if strings.Contains(response.Body.String(), `"name":"list_hosts"`) {
		t.Fatalf("read token saw host tools: %s", response.Body.String())
	}

	projectID := "project"
	list = withMCPIdentity(
		mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`),
		automation.Identity{TokenID: "bound", Role: "admin", ProjectID: &projectID},
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if !strings.Contains(response.Body.String(), `"name":"list_hosts"`) {
		t.Fatalf("project-bound admin missing list_hosts: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"name":"create_host_join_token"`) {
		t.Fatalf("project-bound admin saw join token tool: %s", response.Body.String())
	}

	list = withMCPIdentity(
		mcpRequest(`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`),
		automation.Identity{TokenID: "root", Role: "admin"},
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if !strings.Contains(response.Body.String(), `"name":"list_hosts"`) ||
		!strings.Contains(response.Body.String(), `"name":"create_host_join_token"`) ||
		!strings.Contains(response.Body.String(), `"name":"delete_host"`) {
		t.Fatalf("unbound admin host tools = %s", response.Body.String())
	}

	call := withMCPIdentity(
		mcpRequest(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_hosts","arguments":{}}}`),
		automation.Identity{TokenID: "root", Role: "admin"},
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) ||
		!strings.Contains(response.Body.String(), `edge-1`) {
		t.Fatalf("list_hosts = %s", response.Body.String())
	}
}
