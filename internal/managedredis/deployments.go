package managedredis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

func (controller *Controller) prepareRuntimeDeployment(ctx context.Context, resource state.ManagedRedis) (string, bool, error) {
	if controller.deployments == nil {
		return resource.ID, false, nil
	}
	current, err := controller.deployments.ActiveRuntimeDeployment(ctx, "redis", resource.ID)
	if err == nil && current.ImageDigest == resource.ImageDigest {
		return current.ID, current.Status == "removed", nil
	}
	if err != nil && !errors.Is(err, state.ErrRuntimeDeploymentNotFound) {
		return "", false, err
	}
	deploymentID, err := controller.newID(controller.now())
	if err != nil {
		return "", false, fmt.Errorf("allocate managed Redis deployment ID: %w", err)
	}
	if err := controller.deployments.BeginRuntimeDeployment(ctx, state.RuntimeDeployment{
		ID: deploymentID, ResourceKind: "redis", ResourceID: resource.ID,
		ImageTag: resource.ImageTag, ImageDigest: resource.ImageDigest,
		CreatedAtMillis: controller.now().UnixMilli(),
	}); err != nil {
		return "", false, err
	}
	return deploymentID, false, nil
}

func (controller *Controller) beginCandidateDeployment(ctx context.Context, deploymentID string, resource state.ManagedRedis, createdAt int64) error {
	if controller.deployments == nil {
		return nil
	}
	return controller.deployments.BeginRuntimeDeployment(ctx, state.RuntimeDeployment{
		ID: deploymentID, ResourceKind: "redis", ResourceID: resource.ID,
		ImageTag: resource.ImageTag, ImageDigest: resource.ImageDigest, CreatedAtMillis: createdAt,
	})
}

func (controller *Controller) finishCandidateDeployment(ctx context.Context, deploymentID string, resultErr *error) {
	if controller.deployments == nil || *resultErr == nil {
		return
	}
	*resultErr = errors.Join(*resultErr, controller.recordDeploymentFailure(deploymentID, "deployment_failed", (*resultErr).Error()))
}

func (controller *Controller) recordDeploymentFailure(deploymentID, code, message string) error {
	if controller.deployments == nil {
		return nil
	}
	writeContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return controller.deployments.FailRuntimeDeployment(writeContext, deploymentID, code, message, controller.now().UnixMilli())
}

func (controller *Controller) activateCandidateDeployment(ctx context.Context, resourceID, deploymentID string) error {
	if controller.deployments == nil {
		return nil
	}
	return controller.deployments.ActivateRuntimeDeployment(ctx, "redis", resourceID, deploymentID, controller.now().UnixMilli())
}

func (controller *Controller) RestartDeployment(ctx context.Context, resourceID, deploymentID string) error {
	if controller.deployments == nil {
		return errors.New("managed Redis deployment history is unavailable")
	}
	current, err := controller.deployments.ActiveRuntimeDeployment(ctx, "redis", resourceID)
	if err != nil {
		return err
	}
	if current.ID != deploymentID {
		return state.ErrRuntimeDeploymentNotFound
	}
	if err := controller.Stop(ctx, resourceID); err != nil {
		return err
	}
	if err := controller.deployments.RestartRuntimeDeployment(ctx, "redis", resourceID, deploymentID); err != nil {
		return err
	}
	return controller.Start(ctx, resourceID)
}

func (controller *Controller) RemoveDeployment(ctx context.Context, resourceID, deploymentID string) error {
	if controller.deployments == nil {
		return errors.New("managed Redis deployment history is unavailable")
	}
	deployment, err := controller.deployments.RuntimeDeployment(ctx, "redis", resourceID, deploymentID)
	if err != nil {
		return err
	}
	if deployment.Active {
		if err := controller.Stop(ctx, resourceID); err != nil {
			return err
		}
		return controller.deployments.StopRuntimeDeployment(ctx, "redis", resourceID, deploymentID, controller.now().UnixMilli())
	}
	if !safePathComponent(resourceID) || !safePathComponent(deploymentID) {
		return state.ErrRuntimeDeploymentInvalid
	}
	if err := os.RemoveAll(filepath.Join(controller.logRoot, "redis", resourceID, deploymentID)); err != nil {
		return fmt.Errorf("remove managed Redis deployment logs: %w", err)
	}
	return controller.deployments.DeleteRuntimeDeployment(ctx, "redis", resourceID, deploymentID)
}
