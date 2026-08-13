package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/portforward"
)

func portForwardAdminTool() Tool {
	return Tool{
		Name:        "create_port_forward",
		Description: "Create a short-lived localhost tunnel to a resource TCP port or to a service's errors endpoint. Returns installation and connection commands. Requires an admin token.",
		InputSchema: objectSchema(map[string]any{
			"project":          map[string]any{"type": "string"},
			"resource":         map[string]any{"type": "string"},
			"endpoint":         map[string]any{"type": "string", "enum": []string{portforward.EndpointErrors}},
			"port":             map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
			"localPort":        map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
			"expiresInSeconds": map[string]any{"type": "integer", "minimum": 60, "maximum": 28800},
		}, []string{"project", "resource"}),
	}
}

func (handler *Handler) createPortForward(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		Project          string `json:"project"`
		Resource         string `json:"resource"`
		Endpoint         string `json:"endpoint"`
		Port             int    `json:"port"`
		LocalPort        int    `json:"localPort"`
		ExpiresInSeconds int    `json:"expiresInSeconds"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	localPort := input.LocalPort
	if localPort == 0 {
		if input.Endpoint == portforward.EndpointErrors {
			localPort = firewall.ServiceTelemetryPort
		} else {
			localPort = input.Port
		}
	}
	if localPort < 1 || localPort > 65535 {
		return nil, fmt.Errorf("%w: localPort must be from 1 to 65535", errInvalidArguments)
	}
	grant, err := handler.portForwards.Create(ctx, identity, portforward.CreateInput{
		Project: input.Project, Resource: input.Resource,
		Endpoint: input.Endpoint, Port: input.Port, LifetimeSeconds: input.ExpiresInSeconds,
	})
	if err != nil {
		return nil, err
	}
	response := map[string]any{
		"id": grant.ID, "ticket": grant.Ticket, "project": grant.Project,
		"resource": grant.Resource, "resourceKind": grant.ResourceKind,
		"port":         grant.Port,
		"expiresAt":    grant.ExpiresAt.Format(time.RFC3339),
		"instructions": portforward.ConnectionInstructions(handler.hostname, grant.Ticket, localPort, grant.EndpointHost),
	}
	if grant.Endpoint != "" {
		response["endpoint"] = grant.Endpoint
		response["endpointHost"] = grant.EndpointHost
	}
	return response, nil
}
