package daemon

import (
	"context"
	"testing"

	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/githubapp"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
)

type fakeGitHubDeploymentClient struct {
	created  []githubapp.CreateDeploymentInput
	statuses []githubapp.CreateDeploymentStatusInput
}

func (client *fakeGitHubDeploymentClient) CreateDeployment(
	_ context.Context,
	input githubapp.CreateDeploymentInput,
) (githubapp.Deployment, error) {
	client.created = append(client.created, input)
	return githubapp.Deployment{ID: 42}, nil
}

func (client *fakeGitHubDeploymentClient) CreateDeploymentStatus(
	_ context.Context,
	input githubapp.CreateDeploymentStatusInput,
) error {
	client.statuses = append(client.statuses, input)
	return nil
}

func TestGitHubDeploymentReporterCreatesDeploymentAndStatuses(t *testing.T) {
	client := &fakeGitHubDeploymentClient{}
	reporter := githubDeploymentReporter{github: client, adminHostname: "admin.example.com"}
	desired := state.ServiceDesired{
		ID: "service-id", ProjectID: "project-id", ProjectName: "storefront", Name: "api",
		Snapshot: serviceconfig.Snapshot{Source: servicesource.Source{
			Type:   servicesource.GitHubImage,
			GitHub: &servicesource.GitHub{RepositoryID: 7, Repository: "acme/api", Branch: "main"},
		}},
	}
	reportID, err := reporter.Start(context.Background(), desired, "local-deployment", "commit-sha")
	if err != nil {
		t.Fatal(err)
	}
	if reportID != "42" || len(client.created) != 1 {
		t.Fatalf("deployment = %q / %+v", reportID, client.created)
	}
	created := client.created[0]
	if created.RepositoryID != 7 || created.Ref != "commit-sha" || created.Environment != "platformd/storefront/api" || created.PlatformdDeploymentID != "local-deployment" || !created.ProductionEnvironment || created.TransientEnvironment {
		t.Fatalf("create input = %+v", created)
	}
	if len(client.statuses) != 1 || client.statuses[0].State != githubapp.DeploymentInProgress || client.statuses[0].LogURL != "https://admin.example.com/projects/project-id/services/service-id/deployments/local-deployment/deploy-logs" {
		t.Fatalf("start status = %+v", client.statuses)
	}
	if err := reporter.Finish(context.Background(), desired, "local-deployment", reportID, deployment.ReportSucceeded); err != nil {
		t.Fatal(err)
	}
	if len(client.statuses) != 2 || client.statuses[1].State != githubapp.DeploymentSuccess || client.statuses[1].DeploymentID != 42 {
		t.Fatalf("finish status = %+v", client.statuses)
	}
}
