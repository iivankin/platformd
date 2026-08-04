package daemon

import (
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/deployment"
)

func TestClassifyServiceStatusKeepsHealthyRuntimeVisibleDuringPullFailure(t *testing.T) {
	status, message := classifyServiceStatus(
		true,
		deployment.RuntimeStatus{DeploymentID: "deployment", State: "running"},
		true,
		nil,
		errors.New("registry temporarily unavailable"),
	)
	if status != "degraded" || message != "registry temporarily unavailable" {
		t.Fatalf("status/message = %q/%q", status, message)
	}
}

func TestClassifyServiceStatusReportsFirstDeploymentFailure(t *testing.T) {
	status, message := classifyServiceStatus(
		true,
		deployment.RuntimeStatus{},
		false,
		nil,
		errors.New("image pull failed"),
	)
	if status != "failed" || message != "image pull failed" {
		t.Fatalf("status/message = %q/%q", status, message)
	}
}

func TestSuccessfulDeploymentClearsPreviousFailure(t *testing.T) {
	stack := &runtimeStack{
		serviceFailures: map[string]error{
			"service": errors.New("previous build failed"),
		},
	}

	stack.recordServiceResult("service", nil)

	status, message := classifyServiceStatus(
		true,
		deployment.RuntimeStatus{DeploymentID: "deployment", State: "running"},
		true,
		nil,
		stack.serviceFailures["service"],
	)
	if status != "running" || message != "" {
		t.Fatalf("status/message = %q/%q", status, message)
	}
}

func TestServiceBackendUsesImmutablePublicationSnapshot(t *testing.T) {
	stack := &runtimeStack{}
	stack.publishBackendLocked("service-a", publishedBackend{deploymentID: "deployment-a", address: "10.0.0.2"})
	first := stack.publishedBackends.Load()
	stack.publishBackendLocked("service-b", publishedBackend{deploymentID: "deployment-b", address: "10.0.0.3"})

	if _, exists := first.services["service-b"]; exists {
		t.Fatal("publishing a backend mutated the previous snapshot")
	}
	backend, available, err := stack.ServiceBackend("service-a", 8080)
	if err != nil || !available || backend.DeploymentID != "deployment-a" || backend.Address != "10.0.0.2" || backend.Port != 8080 {
		t.Fatalf("published backend = %+v, %t, %v", backend, available, err)
	}
	stack.withdrawBackendLocked("service-a")
	if _, available, err := stack.ServiceBackend("service-a", 8080); err != nil || available {
		t.Fatalf("withdrawn backend remained available: %t, %v", available, err)
	}
	if _, available, err := stack.ServiceBackend("service-b", 9090); err != nil || !available {
		t.Fatalf("independent backend was lost: %t, %v", available, err)
	}
}
