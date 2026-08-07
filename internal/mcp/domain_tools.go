package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/iivankin/platformd/internal/automation"
)

func listServiceDomainsTool() Tool {
	return Tool{
		Name: "list_service_domains", Description: "List hostnames attached to one service.",
		InputSchema: objectSchema(map[string]any{
			"projectId": map[string]any{"type": "string"},
			"serviceId": map[string]any{"type": "string"},
		}, []string{"projectId", "serviceId"}),
	}
}

func domainAdminTools() []Tool {
	base := map[string]any{
		"projectId": map[string]any{"type": "string"},
		"serviceId": map[string]any{"type": "string"},
		"hostname":  map[string]any{"type": "string"},
	}
	return []Tool{
		{
			Name: "attach_service_domain", Description: "Attach a hostname to a service. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": base["projectId"], "serviceId": base["serviceId"], "hostname": base["hostname"],
				"targetPort": map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
				"move":       map[string]any{"type": "boolean"},
			}, []string{"projectId", "serviceId", "hostname", "targetPort"}),
		},
		{
			Name: "detach_service_domain", Description: "Detach a hostname from a service. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": base["projectId"], "serviceId": base["serviceId"], "hostname": base["hostname"],
			}, []string{"projectId", "serviceId", "hostname"}),
		},
	}
}

func (handler *Handler) listServiceDomains(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		ServiceID string `json:"serviceId"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" || input.ServiceID == "" {
		return nil, fmt.Errorf("%w: projectId and serviceId are required", errInvalidArguments)
	}
	domains, err := handler.domains.List(ctx, identity, input.ProjectID, input.ServiceID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"domains": domains}, nil
}

func (handler *Handler) attachServiceDomain(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID  string `json:"projectId"`
		ServiceID  string `json:"serviceId"`
		Hostname   string `json:"hostname"`
		TargetPort int    `json:"targetPort"`
		Move       bool   `json:"move"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	result, err := handler.domains.Attach(ctx, identity, automation.AttachDomainInput{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, Hostname: input.Hostname,
		TargetPort: input.TargetPort, Move: input.Move,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"domain": result.Domain, "requestId": result.RequestID}, nil
}

func (handler *Handler) detachServiceDomain(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		ServiceID string `json:"serviceId"`
		Hostname  string `json:"hostname"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	result, err := handler.domains.Detach(ctx, identity, automation.DetachDomainInput{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, Hostname: input.Hostname,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"requestId": result.RequestID}, nil
}
