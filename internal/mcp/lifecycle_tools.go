package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/state"
)

func lifecycleAdminTools() []Tool {
	base := map[string]any{
		"projectId":         map[string]any{"type": "string"},
		"serviceId":         map[string]any{"type": "string"},
		"expectedUpdatedAt": map[string]any{"type": "integer"},
	}
	return []Tool{
		{
			Name: "delete_service", Description: "Delete a service. Requires an admin token.",
			InputSchema: objectSchema(base, []string{"projectId", "serviceId", "expectedUpdatedAt"}),
		},
		{
			Name: "restart_service_deployment", Description: "Restart the active service deployment. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": base["projectId"], "serviceId": base["serviceId"],
				"deploymentId": map[string]any{"type": "string"}, "expectedUpdatedAt": base["expectedUpdatedAt"],
			}, []string{"projectId", "serviceId", "deploymentId", "expectedUpdatedAt"}),
		},
		{
			Name: "remove_service_deployment", Description: "Remove one service deployment history entry. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": base["projectId"], "serviceId": base["serviceId"],
				"deploymentId": map[string]any{"type": "string"}, "expectedUpdatedAt": base["expectedUpdatedAt"],
			}, []string{"projectId", "serviceId", "deploymentId", "expectedUpdatedAt"}),
		},
	}
}

func managedDeploymentAdminTools() []Tool {
	return []Tool{
		{
			Name: "restart_managed_deployment", Description: "Restart one Redis or PostgreSQL deployment. Requires an admin token.",
			InputSchema: managedDeploymentSchema(),
		},
		{
			Name: "remove_managed_deployment", Description: "Remove one Redis or PostgreSQL deployment. Requires an admin token.",
			InputSchema: managedDeploymentSchema(),
		},
	}
}

func managedDeploymentSchema() map[string]any {
	return objectSchema(map[string]any{
		"projectId":    map[string]any{"type": "string"},
		"kind":         map[string]any{"type": "string", "enum": []string{"redis", "postgres"}},
		"resourceId":   map[string]any{"type": "string"},
		"deploymentId": map[string]any{"type": "string"},
	}, []string{"projectId", "kind", "resourceId", "deploymentId"})
}

func networkGatewayReadTools() []Tool {
	return []Tool{
		{
			Name: "list_network_gateways", Description: "List network gateways in one visible project.",
			InputSchema: objectSchema(map[string]any{"projectId": map[string]any{"type": "string"}}, []string{"projectId"}),
		},
		{
			Name: "get_network_gateway", Description: "Read one network gateway.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"}, "gatewayId": map[string]any{"type": "string"},
			}, []string{"projectId", "gatewayId"}),
		},
		{
			Name: "list_network_addresses", Description: "List host network addresses available for gateways.",
			InputSchema: objectSchema(nil, nil),
		},
	}
}

func networkGatewayAdminTools() []Tool {
	config := map[string]any{
		"name":            map[string]any{"type": "string"},
		"mode":            map[string]any{"type": "string"},
		"transport":       map[string]any{"type": "string"},
		"protocol":        map[string]any{"type": "string"},
		"interfaceName":   map[string]any{"type": "string"},
		"sourceAddress":   map[string]any{"type": "string"},
		"listenPort":      map[string]any{"type": "integer"},
		"remoteHost":      map[string]any{"type": "string"},
		"remotePort":      map[string]any{"type": "integer"},
		"targetServiceId": map[string]any{"type": "string"},
		"targetPort":      map[string]any{"type": "integer"},
	}
	return []Tool{
		{
			Name: "create_network_gateway", Description: "Create a network gateway. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"}, "configuration": map[string]any{
					"type": "object", "properties": config, "additionalProperties": false, "required": []string{"name", "mode"},
				},
			}, []string{"projectId", "configuration"}),
		},
		{
			Name: "update_network_gateway", Description: "Update a network gateway. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"}, "gatewayId": map[string]any{"type": "string"},
				"configuration": map[string]any{"type": "object", "properties": config, "additionalProperties": false, "required": []string{"name", "mode"}},
			}, []string{"projectId", "gatewayId", "configuration"}),
		},
		{
			Name: "delete_network_gateway", Description: "Delete a network gateway. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"}, "gatewayId": map[string]any{"type": "string"},
			}, []string{"projectId", "gatewayId"}),
		},
	}
}

func (handler *Handler) deleteService(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID         string `json:"projectId"`
		ServiceID         string `json:"serviceId"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	result, err := handler.services.Delete(ctx, identity, automation.DeleteServiceInput{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, ExpectedUpdatedAt: input.ExpectedUpdatedAt,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"requestId": result.RequestID}, nil
}

func (handler *Handler) restartServiceDeployment(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	return handler.serviceDeploymentAction(ctx, arguments, identity, handler.services.RestartDeployment)
}

func (handler *Handler) removeServiceDeployment(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	return handler.serviceDeploymentAction(ctx, arguments, identity, handler.services.RemoveDeployment)
}

func (handler *Handler) serviceDeploymentAction(
	ctx context.Context,
	arguments json.RawMessage,
	identity automation.Identity,
	action func(context.Context, automation.Identity, automation.ServiceDeploymentActionInput) (automation.ServiceMutationResult, error),
) (any, error) {
	var input struct {
		ProjectID         string `json:"projectId"`
		ServiceID         string `json:"serviceId"`
		DeploymentID      string `json:"deploymentId"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	result, err := action(ctx, identity, automation.ServiceDeploymentActionInput{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, DeploymentID: input.DeploymentID,
		ExpectedUpdatedAt: input.ExpectedUpdatedAt,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"service": publicService(result.Service), "requestId": result.RequestID}, nil
}

func (handler *Handler) restartManagedDeployment(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	input, err := managedDeploymentArguments(arguments)
	if err != nil {
		return nil, err
	}
	if err := handler.managedDeployments.Restart(ctx, identity, input); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (handler *Handler) removeManagedDeployment(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	input, err := managedDeploymentArguments(arguments)
	if err != nil {
		return nil, err
	}
	if err := handler.managedDeployments.Remove(ctx, identity, input); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func managedDeploymentArguments(arguments json.RawMessage) (automation.ManagedDeploymentActionInput, error) {
	var input struct {
		ProjectID    string `json:"projectId"`
		Kind         string `json:"kind"`
		ResourceID   string `json:"resourceId"`
		DeploymentID string `json:"deploymentId"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return automation.ManagedDeploymentActionInput{}, err
	}
	return automation.ManagedDeploymentActionInput{
		ProjectID: input.ProjectID, Kind: input.Kind, ResourceID: input.ResourceID, DeploymentID: input.DeploymentID,
	}, nil
}

func (handler *Handler) listNetworkGateways(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" {
		return nil, fmt.Errorf("%w: projectId is required", errInvalidArguments)
	}
	gateways, err := handler.networkGateways.List(ctx, identity, input.ProjectID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"gateways": gateways}, nil
}

func (handler *Handler) getNetworkGateway(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		GatewayID string `json:"gatewayId"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" || input.GatewayID == "" {
		return nil, fmt.Errorf("%w: projectId and gatewayId are required", errInvalidArguments)
	}
	gateway, err := handler.networkGateways.Get(ctx, identity, input.ProjectID, input.GatewayID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"gateway": gateway}, nil
}

func (handler *Handler) listNetworkAddresses(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var empty struct{}
	if err := decodeArguments(arguments, &empty); err != nil {
		return nil, err
	}
	addresses, err := handler.networkGateways.ListAddresses(ctx, identity)
	if err != nil {
		return nil, err
	}
	return map[string]any{"addresses": addresses}, nil
}

func (handler *Handler) createNetworkGateway(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	input, err := networkGatewayMutationArguments(arguments)
	if err != nil {
		return nil, err
	}
	result, err := handler.networkGateways.Create(ctx, identity, input)
	if err != nil {
		return nil, err
	}
	return map[string]any{"gateway": result.Gateway, "requestId": result.RequestID}, nil
}

func (handler *Handler) updateNetworkGateway(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	input, err := networkGatewayMutationArguments(arguments)
	if err != nil {
		return nil, err
	}
	result, err := handler.networkGateways.Update(ctx, identity, input)
	if err != nil {
		return nil, err
	}
	return map[string]any{"gateway": result.Gateway, "requestId": result.RequestID}, nil
}

func networkGatewayMutationArguments(arguments json.RawMessage) (automation.NetworkGatewayMutationInput, error) {
	var input struct {
		ProjectID     string `json:"projectId"`
		GatewayID     string `json:"gatewayId"`
		Configuration struct {
			Name            string `json:"name"`
			Mode            string `json:"mode"`
			Transport       string `json:"transport"`
			Protocol        string `json:"protocol"`
			InterfaceName   string `json:"interfaceName"`
			SourceAddress   string `json:"sourceAddress"`
			ListenPort      int    `json:"listenPort"`
			RemoteHost      string `json:"remoteHost"`
			RemotePort      int    `json:"remotePort"`
			TargetServiceID string `json:"targetServiceId"`
			TargetPort      int    `json:"targetPort"`
		} `json:"configuration"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return automation.NetworkGatewayMutationInput{}, err
	}
	return automation.NetworkGatewayMutationInput{
		ProjectID: input.ProjectID, GatewayID: input.GatewayID,
		Config: state.NetworkGatewayConfiguration{
			Name: input.Configuration.Name, Mode: input.Configuration.Mode, Transport: input.Configuration.Transport,
			Protocol: input.Configuration.Protocol, InterfaceName: input.Configuration.InterfaceName,
			SourceAddress: input.Configuration.SourceAddress, ListenPort: input.Configuration.ListenPort,
			RemoteHost: input.Configuration.RemoteHost, RemotePort: input.Configuration.RemotePort,
			TargetServiceID: input.Configuration.TargetServiceID, TargetPort: input.Configuration.TargetPort,
		},
	}, nil
}

func (handler *Handler) deleteNetworkGateway(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		GatewayID string `json:"gatewayId"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	requestID, err := handler.networkGateways.Delete(ctx, identity, input.ProjectID, input.GatewayID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"requestId": requestID}, nil
}
