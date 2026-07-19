package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/githubapp"
	"github.com/iivankin/platformd/internal/state"
)

type githubDeploymentClient interface {
	CreateDeployment(context.Context, githubapp.CreateDeploymentInput) (githubapp.Deployment, error)
	CreateDeploymentStatus(context.Context, githubapp.CreateDeploymentStatusInput) error
}

type githubDeploymentReporter struct {
	github        githubDeploymentClient
	adminHostname string
}

func (reporter githubDeploymentReporter) Start(
	ctx context.Context,
	desired state.ServiceDesired,
	deploymentID string,
	revision string,
) (string, error) {
	source := desired.Snapshot.Source.GitHub
	if source == nil {
		return "", nil
	}
	if reporter.github == nil {
		return "", errors.New("GitHub App is not configured")
	}
	environment := githubEnvironment(desired)
	created, err := reporter.github.CreateDeployment(ctx, githubapp.CreateDeploymentInput{
		RepositoryID: source.RepositoryID, Ref: revision, Environment: environment,
		Description:           "Deploy " + desired.ProjectName + "/" + desired.Name + " with platformd",
		PlatformdDeploymentID: deploymentID, ProductionEnvironment: true,
	})
	if err != nil {
		return "", err
	}
	reportID := strconv.FormatInt(created.ID, 10)
	err = reporter.github.CreateDeploymentStatus(ctx, githubapp.CreateDeploymentStatusInput{
		RepositoryID: source.RepositoryID, DeploymentID: created.ID,
		State: githubapp.DeploymentInProgress, Environment: environment,
		Description: "Deployment started", LogURL: reporter.logURL(desired, deploymentID),
	})
	return reportID, err
}

func (reporter githubDeploymentReporter) Finish(
	ctx context.Context,
	desired state.ServiceDesired,
	localDeploymentID string,
	reportID string,
	status deployment.ReportStatus,
) error {
	source := desired.Snapshot.Source.GitHub
	if source == nil || reportID == "" {
		return nil
	}
	deploymentID, err := strconv.ParseInt(reportID, 10, 64)
	if err != nil || deploymentID <= 0 {
		return errors.New("GitHub deployment report ID is invalid")
	}
	githubStatus := githubapp.DeploymentFailure
	description := "Deployment failed"
	if status == deployment.ReportSucceeded {
		githubStatus = githubapp.DeploymentSuccess
		description = "Deployment succeeded"
	}
	return reporter.github.CreateDeploymentStatus(ctx, githubapp.CreateDeploymentStatusInput{
		RepositoryID: source.RepositoryID, DeploymentID: deploymentID,
		State: githubStatus, Environment: githubEnvironment(desired),
		Description: description, LogURL: reporter.logURL(desired, localDeploymentID),
	})
}

func githubEnvironment(desired state.ServiceDesired) string {
	return "platformd/" + desired.ProjectName + "/" + desired.Name
}

func (reporter githubDeploymentReporter) logURL(desired state.ServiceDesired, deploymentID string) string {
	value, err := url.JoinPath(
		"https://"+reporter.adminHostname,
		"projects", desired.ProjectID, "services", desired.ID, "deployments", deploymentID, "deploy-logs",
	)
	if err != nil {
		return fmt.Sprintf(
			"https://%s/projects/%s/services/%s/deployments/%s/deploy-logs",
			reporter.adminHostname, desired.ProjectID, desired.ID, deploymentID,
		)
	}
	return value
}
