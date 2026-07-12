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
