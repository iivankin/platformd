package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

type HostHub interface {
	Hosts(context.Context) ([]state.Host, error)
	Connected(string) bool
	ActiveHostJoinTokens(context.Context) ([]state.HostJoinToken, error)
	CreateJoinToken(context.Context, string, string, string, string) (string, state.HostJoinToken, error)
	JoinCommand(string) string
	DeleteHost(context.Context, state.DeleteHostInput) error
	DeleteJoinToken(context.Context, state.DeleteHostJoinTokenInput) error
}

func listHostsTool() Tool {
	return Tool{
		Name:        "list_hosts",
		Description: "List child servers that can run services. The primary VPS is omitted; pass an empty hostId to keep a service there. Requires an admin token.",
		InputSchema: objectSchema(map[string]any{}, nil),
	}
}

func hostAdminMutationTools() []Tool {
	return []Tool{
		{
			Name:        "create_host_join_token",
			Description: "Create a one-time join token and a copyable worker install plus `platformd join` command. Only visible to an unbound admin token.",
			InputSchema: objectSchema(map[string]any{
				"name": map[string]any{"type": "string", "description": "Lowercase DNS label stored on the child after join"},
			}, []string{"name"}),
		},
		{
			Name:        "delete_host",
			Description: "Remove a child server that has no assigned services. Only visible to an unbound admin token.",
			InputSchema: objectSchema(map[string]any{
				"hostId": map[string]any{"type": "string", "description": "Exact host ID from list_hosts"},
			}, []string{"hostId"}),
		},
		{
			Name:        "delete_host_join_token",
			Description: "Revoke an unused join token. Only visible to an unbound admin token.",
			InputSchema: objectSchema(map[string]any{
				"tokenId": map[string]any{"type": "string", "description": "Exact token ID from list_hosts.joinTokens"},
			}, []string{"tokenId"}),
		},
	}
}

type hostOutput struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PublicIPv4 string `json:"publicIpv4,omitempty"`
	LastSeenAt *int64 `json:"lastSeenAt,omitempty"`
	JoinedAt   int64  `json:"joinedAt"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
	Connected  bool   `json:"connected"`
}

func requireUnboundHostAdmin(identity automation.Identity) error {
	if !identity.IsAdmin() {
		return automation.ErrAdminRequired
	}
	if identity.ProjectID != nil {
		return automation.ErrUnboundAdminRequired
	}
	return nil
}

func (handler *Handler) listHosts(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	if !identity.IsAdmin() {
		return nil, automation.ErrAdminRequired
	}
	var empty struct{}
	if err := decodeArguments(arguments, &empty); err != nil {
		return nil, fmt.Errorf("%w: list_hosts requires an empty object", errInvalidArguments)
	}
	hosts, err := handler.hosts.Hosts(ctx)
	if err != nil {
		return nil, err
	}
	tokens, err := handler.hosts.ActiveHostJoinTokens(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]hostOutput, 0, len(hosts))
	for _, host := range hosts {
		result = append(result, hostOutput{
			ID: host.ID, Name: host.Name, PublicIPv4: host.PublicIPv4,
			LastSeenAt: host.LastSeenMillis, JoinedAt: host.JoinedAtMillis,
			CreatedAt: host.CreatedAtMillis, UpdatedAt: host.UpdatedAtMillis,
			Connected: handler.hosts.Connected(host.ID),
		})
	}
	pending := make([]map[string]any, 0, len(tokens))
	for _, token := range tokens {
		pending = append(pending, map[string]any{
			"id": token.ID, "name": token.Name, "expiresAt": token.ExpiresAtMillis, "createdAt": token.CreatedAtMillis,
		})
	}
	return map[string]any{"hosts": result, "joinTokens": pending}, nil
}

func (handler *Handler) createHostJoinToken(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if err := requireUnboundHostAdmin(identity); err != nil {
		return nil, err
	}
	requestID, err := id.New()
	if err != nil {
		return nil, err
	}
	plaintext, token, err := handler.hosts.CreateJoinToken(ctx, input.Name, identity.TokenID, "", requestID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": token.ID, "name": token.Name, "token": plaintext,
		"command":   handler.hosts.JoinCommand(plaintext),
		"expiresAt": token.ExpiresAtMillis, "createdAt": token.CreatedAtMillis, "requestId": requestID,
	}, nil
}

func (handler *Handler) deleteHost(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		HostID string `json:"hostId"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.HostID == "" {
		return nil, fmt.Errorf("%w: hostId is required", errInvalidArguments)
	}
	if err := requireUnboundHostAdmin(identity); err != nil {
		return nil, err
	}
	auditID, err := id.New()
	if err != nil {
		return nil, err
	}
	requestID, err := id.New()
	if err != nil {
		return nil, err
	}
	if err := handler.hosts.DeleteHost(ctx, state.DeleteHostInput{
		ID: input.HostID, AuditEventID: auditID, ActorKind: "token", ActorID: identity.TokenID,
		RequestCorrelationID: requestID, DeletedAtMillis: time.Now().UnixMilli(),
	}); err != nil {
		return nil, err
	}
	return map[string]any{"requestId": requestID}, nil
}

func (handler *Handler) deleteHostJoinToken(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		TokenID string `json:"tokenId"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.TokenID == "" {
		return nil, fmt.Errorf("%w: tokenId is required", errInvalidArguments)
	}
	if err := requireUnboundHostAdmin(identity); err != nil {
		return nil, err
	}
	auditID, err := id.New()
	if err != nil {
		return nil, err
	}
	requestID, err := id.New()
	if err != nil {
		return nil, err
	}
	if err := handler.hosts.DeleteJoinToken(ctx, state.DeleteHostJoinTokenInput{
		ID: input.TokenID, AuditEventID: auditID, ActorKind: "token", ActorID: identity.TokenID,
		RequestCorrelationID: requestID, DeletedAtMillis: time.Now().UnixMilli(),
	}); err != nil {
		return nil, err
	}
	return map[string]any{"requestId": requestID}, nil
}
