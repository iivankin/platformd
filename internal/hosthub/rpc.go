package hosthub

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/iivankin/platformd/internal/hostconn"
	"github.com/iivankin/platformd/internal/state"
)

func (hub *Hub) handleRPC(ctx context.Context, hostID string, envelope hostconn.Envelope) error {
	var request hostconn.RPCRequest
	if err := json.Unmarshal(envelope.Payload, &request); err != nil {
		return err
	}
	payload, err := hub.dispatchRPC(ctx, hostID, request)
	result := hostconn.Envelope{ID: envelope.ID, Kind: hostconn.KindRPCResult}
	if err != nil {
		result.Error = err.Error()
	} else if payload != nil {
		body, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return marshalErr
		}
		result.Payload = body
	}
	return hub.reply(ctx, hostID, result)
}

func (hub *Hub) dispatchRPC(ctx context.Context, hostID string, request hostconn.RPCRequest) (any, error) {
	switch request.Method {
	case hostconn.RPCDesiredService:
		var params hostconn.ServiceIDParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if _, err := hub.requireHostService(ctx, hostID, params.ServiceID); err != nil {
			return nil, err
		}
		return hub.store.DesiredService(ctx, params.ServiceID)
	case hostconn.RPCBeginDeployment:
		var params hostconn.BeginDeploymentParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if _, err := hub.requireHostService(ctx, hostID, params.Input.ServiceID); err != nil {
			return nil, err
		}
		return nil, hub.store.BeginDeployment(ctx, params.Input)
	case hostconn.RPCDiscardDeployment:
		var params hostconn.DiscardDeploymentParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if err := hub.requireHostDeployment(ctx, hostID, params.DeploymentID); err != nil {
			return nil, err
		}
		return nil, hub.store.DiscardDeployment(ctx, params.DeploymentID)
	case hostconn.RPCUpdateDeploymentSource:
		var params hostconn.UpdateDeploymentSourceParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if err := hub.requireHostDeployment(ctx, hostID, params.DeploymentID); err != nil {
			return nil, err
		}
		return nil, hub.store.UpdateDeploymentSource(ctx, params.DeploymentID, params.ImageDigest, params.ImageReference, params.SourceRevision, params.CommitMessage)
	case hostconn.RPCFinishDeployment:
		var params hostconn.FinishDeploymentParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if err := hub.requireHostDeployment(ctx, hostID, params.DeploymentID); err != nil {
			return nil, err
		}
		return nil, hub.store.FinishDeployment(ctx, params.DeploymentID, params.Status, params.ErrorCode, params.ErrorMessage, params.FinishedAt)
	case hostconn.RPCActivateDeployment:
		var params hostconn.ActivateDeploymentParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if _, err := hub.requireHostService(ctx, hostID, params.ServiceID); err != nil {
			return nil, err
		}
		return nil, hub.store.ActivateDeployment(ctx, params.ServiceID, params.DeploymentID, params.ExpectedActiveDeploymentID, params.ActivatedAt)
	case hostconn.RPCFailDeployment:
		var params hostconn.FailDeploymentParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if err := hub.requireHostDeployment(ctx, hostID, params.DeploymentID); err != nil {
			return nil, err
		}
		return nil, hub.store.FailDeployment(ctx, params.DeploymentID, params.ErrorCode, params.ErrorMessage, params.FailedAt)
	case hostconn.RPCLatestFailedDeployment:
		var params hostconn.LatestFailedDeploymentParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if _, err := hub.requireHostService(ctx, hostID, params.ServiceID); err != nil {
			return nil, err
		}
		failed, err := hub.store.LatestFailedDeployment(ctx, params.ServiceID, params.ConfigHash, params.ImageRef)
		return map[string]bool{"failed": failed}, err
	case hostconn.RPCLatestUploadedDeployment:
		var params hostconn.ServiceIDParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if _, err := hub.requireHostService(ctx, hostID, params.ServiceID); err != nil {
			return nil, err
		}
		return hub.store.LatestUploadedDeployment(ctx, params.ServiceID)
	case hostconn.RPCLatestReusableProductionRevision:
		var params hostconn.ServiceIDParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if _, err := hub.requireHostService(ctx, hostID, params.ServiceID); err != nil {
			return nil, err
		}
		return hub.store.LatestReusableProductionRevision(ctx, params.ServiceID)
	case hostconn.RPCDeployment:
		var params hostconn.DiscardDeploymentParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if err := hub.requireHostDeployment(ctx, hostID, params.DeploymentID); err != nil {
			return nil, err
		}
		return hub.store.Deployment(ctx, params.DeploymentID)
	case hostconn.RPCVolumeInitialized:
		var params hostconn.VolumeInitializedParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if _, err := hub.requireHostService(ctx, hostID, params.ServiceID); err != nil {
			return nil, err
		}
		ready, err := hub.store.VolumeInitialized(ctx, params.ProjectID, params.ServiceID, params.VolumeID)
		return map[string]bool{"initialized": ready}, err
	case hostconn.RPCRecordVolumeInitialization:
		var params hostconn.RecordVolumeInitializationParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if _, err := hub.requireHostService(ctx, hostID, params.ServiceID); err != nil {
			return nil, err
		}
		return nil, hub.store.RecordVolumeInitialization(ctx, params.ProjectID, params.ServiceID, params.VolumeID, params.At)
	case hostconn.RPCResolveEnvironment:
		var params hostconn.ResolveEnvironmentParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if hub.environment == nil {
			return nil, errors.New("environment resolver is not configured")
		}
		desired, err := hub.requireHostService(ctx, hostID, params.ServiceID)
		if err != nil {
			return nil, err
		}
		return hub.environment.Resolve(ctx, desired, params.Context)
	case hostconn.RPCResolveImageCredential:
		var params hostconn.ServiceIDParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if hub.credentials == nil {
			return nil, errors.New("image credentials are not configured")
		}
		desired, err := hub.requireHostService(ctx, hostID, params.ServiceID)
		if err != nil {
			return nil, err
		}
		return hub.credentials.Resolve(ctx, desired)
	case hostconn.RPCServiceDomains:
		var params hostconn.ServiceDomainsParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if _, err := hub.requireHostService(ctx, hostID, params.ServiceID); err != nil {
			return nil, err
		}
		return hub.store.ServiceDomains(ctx, params.ProjectID, params.ServiceID)
	case hostconn.RPCServiceListeners:
		var params hostconn.ServiceDomainsParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		if _, err := hub.requireHostService(ctx, hostID, params.ServiceID); err != nil {
			return nil, err
		}
		return hub.store.ServiceListeners(ctx, params.ProjectID, params.ServiceID)
	case hostconn.RPCLookupInternal:
		var params hostconn.LookupInternalParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		return hub.store.LookupInternalName(ctx, params.Hostname)
	default:
		return nil, errors.New("unknown host RPC method")
	}
}

func (hub *Hub) requireHostService(ctx context.Context, hostID, serviceID string) (state.ServiceDesired, error) {
	desired, err := hub.store.DesiredService(ctx, serviceID)
	if err != nil {
		return state.ServiceDesired{}, err
	}
	if desired.HostID != hostID {
		return state.ServiceDesired{}, state.ErrServiceNotFound
	}
	return desired, nil
}

func (hub *Hub) requireHostDeployment(ctx context.Context, hostID, deploymentID string) error {
	record, err := hub.store.Deployment(ctx, deploymentID)
	if err != nil {
		return err
	}
	_, err = hub.requireHostService(ctx, hostID, record.ServiceID)
	return err
}
