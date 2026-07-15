package daemon

import (
	"errors"

	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/resourcemetrics"
)

func (stack *runtimeStack) ReadResourceNetwork(kind cgroupstats.Kind, resourceID string) (resourcemetrics.NetworkCounters, bool, error) {
	stack.mu.Lock()
	if stack.closed {
		stack.mu.Unlock()
		return resourcemetrics.NetworkCounters{}, false, errors.New("container runtime is closed")
	}
	engine := stack.engine
	deployments := stack.deployments
	postgres := stack.managedPostgres
	redis := stack.managedRedis
	stack.mu.Unlock()

	container, available, err := metricContainer(kind, resourceID, deployments, postgres, redis)
	if err != nil || !available {
		return resourcemetrics.NetworkCounters{}, available, err
	}
	counters, err := engine.ContainerNetworkCounters(container.ID)
	return resourcemetrics.NetworkCounters{
		RXBytes: counters.RXBytes,
		TXBytes: counters.TXBytes,
	}, err == nil, err
}

func metricContainer(
	kind cgroupstats.Kind,
	resourceID string,
	deployments *deployment.Controller,
	postgres *managedpostgres.Controller,
	redis *managedredis.Controller,
) (containerengine.Container, bool, error) {
	switch kind {
	case cgroupstats.Service:
		if deployments == nil {
			return containerengine.Container{}, false, nil
		}
		return deployments.Container(resourceID)
	case cgroupstats.Postgres:
		if postgres == nil {
			return containerengine.Container{}, false, nil
		}
		return runningMetricContainer(postgres.Status(resourceID))
	case cgroupstats.Redis:
		if redis == nil {
			return containerengine.Container{}, false, nil
		}
		return runningMetricContainer(redis.Status(resourceID))
	default:
		return containerengine.Container{}, false, cgroupstats.ErrInvalidResource
	}
}

func runningMetricContainer(container containerengine.Container, available bool, err error) (containerengine.Container, bool, error) {
	if err != nil || !available {
		return containerengine.Container{}, available, err
	}
	if container.State != "running" {
		return containerengine.Container{}, false, nil
	}
	return container, true, nil
}
