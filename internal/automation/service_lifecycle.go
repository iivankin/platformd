package automation

import (
	"context"
	"fmt"

	"github.com/iivankin/platformd/internal/state"
)

type DeleteServiceInput struct {
	ProjectID         string
	ServiceID         string
	ExpectedUpdatedAt int64
}

type ServiceDeploymentActionInput struct {
	ProjectID         string
	ServiceID         string
	DeploymentID      string
	ExpectedUpdatedAt int64
}

type DeleteServiceResult struct {
	RequestID string
}

func (application *ServiceApplication) Delete(ctx context.Context, identity Identity, input DeleteServiceInput) (DeleteServiceResult, error) {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return DeleteServiceResult{}, err
	}
	if input.ServiceID == "" || input.ExpectedUpdatedAt <= 0 {
		return DeleteServiceResult{}, fmt.Errorf("%w: serviceId and expectedUpdatedAt are required", ErrInvalidInput)
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(2)
	if err != nil {
		return DeleteServiceResult{}, err
	}
	_, err = application.repository.DeleteService(ctx, state.DeleteServiceInput{
		ID: input.ServiceID, ProjectID: input.ProjectID, ExpectedUpdatedMillis: input.ExpectedUpdatedAt,
		AuditEventID: identifiers[0], ActorKind: "token", ActorID: identity.TokenID,
		RequestCorrelationID: identifiers[1], DeletedAtMillis: timestamp.UnixMilli(),
	})
	return DeleteServiceResult{RequestID: identifiers[1]}, err
}

func (application *ServiceApplication) RestartDeployment(ctx context.Context, identity Identity, input ServiceDeploymentActionInput) (ServiceMutationResult, error) {
	return application.deploymentAction(ctx, identity, input, application.repository.RestartServiceDeployment)
}

func (application *ServiceApplication) RemoveDeployment(ctx context.Context, identity Identity, input ServiceDeploymentActionInput) (ServiceMutationResult, error) {
	return application.deploymentAction(ctx, identity, input, application.repository.RemoveServiceDeployment)
}

func (application *ServiceApplication) deploymentAction(
	ctx context.Context,
	identity Identity,
	input ServiceDeploymentActionInput,
	action func(context.Context, state.DeleteServiceDeploymentInput) (state.ServiceDesired, error),
) (ServiceMutationResult, error) {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return ServiceMutationResult{}, err
	}
	if input.ServiceID == "" || input.DeploymentID == "" || input.ExpectedUpdatedAt <= 0 {
		return ServiceMutationResult{}, fmt.Errorf("%w: serviceId, deploymentId, and expectedUpdatedAt are required", ErrInvalidInput)
	}
	identifiers, err := application.identifiers(2)
	if err != nil {
		return ServiceMutationResult{}, err
	}
	service, err := action(ctx, state.DeleteServiceDeploymentInput{
		ID: input.ServiceID, ProjectID: input.ProjectID, DeploymentID: input.DeploymentID,
		ExpectedUpdatedMillis: input.ExpectedUpdatedAt,
		AuditEventID:          identifiers[0], ActorKind: "token", ActorID: identity.TokenID,
		RequestCorrelationID: identifiers[1], CreatedAtMillis: application.now().UnixMilli(),
	})
	return ServiceMutationResult{Service: service, RequestID: identifiers[1]}, err
}
