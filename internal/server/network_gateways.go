package server

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/hostnetwork"
	"github.com/iivankin/platformd/internal/state"
)

const maximumNetworkGatewayRequestBytes = 16 << 10

type NetworkGatewayRepository interface {
	NetworkGateways(context.Context, string) ([]state.NetworkGateway, error)
	NetworkGateway(context.Context, string, string) (state.NetworkGateway, error)
	HostNetworkAddresses(context.Context) ([]hostnetwork.Address, error)
	CreateNetworkGateway(context.Context, state.CreateNetworkGateway) (state.NetworkGateway, error)
	UpdateNetworkGateway(context.Context, state.UpdateNetworkGateway) (state.NetworkGateway, error)
	DeleteNetworkGateway(context.Context, state.DeleteNetworkGateway) error
	ReconcileMeshNetworkGateways(context.Context) error
}

type networkGatewayResponse struct {
	ID               string `json:"id"`
	ProjectID        string `json:"projectId"`
	ProjectName      string `json:"projectName"`
	Name             string `json:"name"`
	Mode             string `json:"mode"`
	Transport        string `json:"transport"`
	Protocol         string `json:"protocol"`
	InterfaceName    string `json:"interfaceName"`
	SourceAddress    string `json:"sourceAddress"`
	ListenPort       int    `json:"listenPort"`
	InternalHostname string `json:"internalHostname,omitempty"`
	RemoteHost       string `json:"remoteHost,omitempty"`
	RemotePort       int    `json:"remotePort,omitempty"`
	TargetServiceID  string `json:"targetServiceId,omitempty"`
	TargetService    string `json:"targetService,omitempty"`
	TargetPort       int    `json:"targetPort,omitempty"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`
}

type networkGatewayRequest struct {
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
}

func (body networkGatewayRequest) configuration() state.NetworkGatewayConfiguration {
	return state.NetworkGatewayConfiguration{
		Name: body.Name, Mode: body.Mode, Transport: body.Transport, Protocol: body.Protocol,
		InterfaceName: body.InterfaceName, SourceAddress: body.SourceAddress,
		ListenPort: body.ListenPort, RemoteHost: body.RemoteHost, RemotePort: body.RemotePort,
		TargetServiceID: body.TargetServiceID, TargetPort: body.TargetPort,
	}
}

func registerNetworkGatewayRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/network/addresses", listHostNetworkAddresses(config.networkGateways))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/network-gateways", listNetworkGateways(config.networkGateways))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/network-gateways", createNetworkGateway(config))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/network-gateways/{gatewayID}", getNetworkGateway(config.networkGateways))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/network-gateways/{gatewayID}", updateNetworkGateway(config))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/network-gateways/{gatewayID}", deleteNetworkGateway(config))
}

func listHostNetworkAddresses(repository NetworkGatewayRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		addresses, err := repository.HostNetworkAddresses(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to inspect host network addresses")
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"addresses": addresses})
	}
}

func listNetworkGateways(repository NetworkGatewayRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		gateways, err := repository.NetworkGateways(request.Context(), request.PathValue("projectID"))
		if err != nil {
			writeNetworkGatewayError(response, err)
			return
		}
		result := make([]networkGatewayResponse, 0, len(gateways))
		for _, gateway := range gateways {
			result = append(result, publicNetworkGateway(gateway))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getNetworkGateway(repository NetworkGatewayRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		gateway, err := repository.NetworkGateway(request.Context(), request.PathValue("projectID"), request.PathValue("gatewayID"))
		if err != nil {
			writeNetworkGatewayError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicNetworkGateway(gateway))
	}
}

func createNetworkGateway(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, body, ok := decodeNetworkGatewayRequest(response, request)
		if !ok {
			return
		}
		timestamp := config.now()
		gatewayID, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate network gateway identifiers")
			return
		}
		created, err := config.networkGateways.CreateNetworkGateway(request.Context(), state.CreateNetworkGateway{
			ID: gatewayID, ProjectID: request.PathValue("projectID"), Configuration: body.configuration(),
			AuditEventID: auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, CreatedAtMillis: timestamp.UnixMilli(),
		})
		if err != nil {
			writeNetworkGatewayError(response, err)
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+created.ProjectID+"/network-gateways/"+created.ID)
		response.Header().Set("X-Request-ID", correlationID)
		writeJSON(response, http.StatusCreated, publicNetworkGateway(created))
	}
}

func updateNetworkGateway(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, body, ok := decodeNetworkGatewayRequest(response, request)
		if !ok {
			return
		}
		timestamp := config.now()
		_, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate network gateway identifiers")
			return
		}
		updated, err := config.networkGateways.UpdateNetworkGateway(request.Context(), state.UpdateNetworkGateway{
			ID: request.PathValue("gatewayID"), ProjectID: request.PathValue("projectID"), Configuration: body.configuration(),
			AuditEventID: auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, UpdatedAtMillis: timestamp.UnixMilli(),
		})
		if err != nil {
			writeNetworkGatewayError(response, err)
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		writeJSON(response, http.StatusOK, publicNetworkGateway(updated))
	}
}

func deleteNetworkGateway(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		timestamp := config.now()
		_, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate network gateway identifiers")
			return
		}
		err = config.networkGateways.DeleteNetworkGateway(request.Context(), state.DeleteNetworkGateway{
			ID: request.PathValue("gatewayID"), ProjectID: request.PathValue("projectID"), AuditEventID: auditID,
			ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, DeletedAtMillis: timestamp.UnixMilli(),
		})
		if err != nil {
			writeNetworkGatewayError(response, err)
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func decodeNetworkGatewayRequest(response http.ResponseWriter, request *http.Request) (access.Identity, networkGatewayRequest, bool) {
	identity, ok := requireAccessIdentity(response, request)
	if !ok {
		return access.Identity{}, networkGatewayRequest{}, false
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return access.Identity{}, networkGatewayRequest{}, false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumNetworkGatewayRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var body networkGatewayRequest
	if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid network gateway fields")
		return access.Identity{}, networkGatewayRequest{}, false
	}
	return identity, body, true
}

func publicNetworkGateway(gateway state.NetworkGateway) networkGatewayResponse {
	result := networkGatewayResponse{
		ID: gateway.ID, ProjectID: gateway.ProjectID, ProjectName: gateway.ProjectName,
		Name: gateway.Name, Mode: gateway.Mode, Transport: gateway.Transport, Protocol: gateway.Protocol,
		InterfaceName: gateway.InterfaceName, SourceAddress: gateway.SourceAddress, ListenPort: gateway.ListenPort,
		RemoteHost: gateway.RemoteHost, RemotePort: gateway.RemotePort,
		TargetServiceID: gateway.TargetServiceID, TargetService: gateway.TargetService, TargetPort: gateway.TargetPort,
		CreatedAt: gateway.CreatedAtMillis, UpdatedAt: gateway.UpdatedAtMillis,
	}
	if gateway.Mode == "import" {
		result.InternalHostname = gateway.Name + "." + gateway.ProjectName + ".internal"
	}
	return result
}

func writeNetworkGatewayError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNetworkGatewayNotFound):
		writeAPIError(response, http.StatusNotFound, "network_gateway_not_found", "Network gateway not found")
	case errors.Is(err, state.ErrProjectNotFound):
		writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrServiceNotFound):
		writeAPIError(response, http.StatusBadRequest, "target_service_not_found", "Target service does not exist in this project")
	case errors.Is(err, state.ErrResourceNameConflict):
		writeAPIError(response, http.StatusConflict, "resource_name_conflict", "A project resource already uses this name")
	case errors.Is(err, state.ErrNetworkGatewaySlotExhausted):
		writeAPIError(response, http.StatusConflict, "network_gateway_capacity", err.Error())
	case errors.Is(err, state.ErrPublicPortUnavailable):
		writeAPIError(response, http.StatusConflict, "network_gateway_port_conflict", "This host address and port are already in use")
	default:
		writeAPIError(response, http.StatusBadRequest, "invalid_network_gateway", err.Error())
	}
}
