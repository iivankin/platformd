package daemon

import (
	"testing"

	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/githubapp"
	"github.com/iivankin/platformd/internal/servicesource"
)

func TestGitHubBuildArgumentsExposeDynamicDeploymentContext(t *testing.T) {
	arguments := githubBuildArguments(
		servicesource.GitHub{Repository: "acme/api"},
		githubapp.Commit{SHA: "abc123", Message: "Build preview"},
		deployment.EnvironmentContext{
			DeploymentID: "preview-42", Kind: deployment.EnvironmentPreview,
			PreviewURL: "https://api-pr-42.example.com", PullRequestNumber: 42,
		},
		"https://api-pr-42.example.com",
	)
	want := map[string]string{
		"PLATFORMD_DEPLOYMENT_ID":           "preview-42",
		"PLATFORMD_GIT_REPOSITORY":          "acme/api",
		"PLATFORMD_GIT_COMMIT_SHA":          "abc123",
		"PLATFORMD_GIT_COMMIT_MESSAGE":      "Build preview",
		"PLATFORMD_PUBLIC_URLS":             "https://api-pr-42.example.com",
		"PLATFORMD_PREVIEW_URL":             "https://api-pr-42.example.com",
		"PLATFORMD_GIT_PULL_REQUEST_NUMBER": "42",
	}
	for name, value := range want {
		if arguments[name] != value {
			t.Fatalf("build argument %s = %q, want %q", name, arguments[name], value)
		}
	}
}
