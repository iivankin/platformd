package serviceconfig

import (
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/servicesource"
)

func TestCanonicalNormalizesAndHashesServiceSnapshot(t *testing.T) {
	input := Snapshot{
		Source: PublicImageSource("alpine"),
		Environment: map[string]string{
			"DATABASE_URL": "postgres://db:5432/app",
			"REDIS_URL":    "${{cache.REDIS_URL}}",
		},
		SecretReferences: []SecretReference{
			{EnvironmentName: "TOKEN", SecretID: "secret-2"},
			{EnvironmentName: "PASSWORD", SecretID: "secret-1"},
		},
		HealthCheck: &HealthCheck{Port: 8080, Path: "/healthz?ready=1"},
		VolumeMounts: []VolumeMount{
			{VolumeID: "volume-2", ContainerPath: "/var/lib/z"},
			{VolumeID: "volume-1", ContainerPath: "/var/lib/a"},
		},
	}
	normalized, encoded, hash, err := Canonical(input)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Source.Image == nil || normalized.Source.Image.Reference != "docker.io/library/alpine:latest" || normalized.HealthCheck == nil || normalized.HealthCheck.TimeoutSeconds != 60 {
		t.Fatalf("normalized snapshot = %+v", normalized)
	}
	if len(hash) != 64 || !strings.Contains(string(encoded), `"containerPath":"/var/lib/a"`) {
		t.Fatalf("encoded/hash = %s/%s", encoded, hash)
	}
	_, secondEncoded, secondHash, err := Canonical(Snapshot{
		Source: PublicImageSource("docker.io/library/alpine:latest"),
		Environment: map[string]string{
			"DATABASE_URL": "postgres://db:5432/app",
			"REDIS_URL":    "${{cache.REDIS_URL}}",
		},
		SecretReferences: []SecretReference{
			{EnvironmentName: "PASSWORD", SecretID: "secret-1"},
			{EnvironmentName: "TOKEN", SecretID: "secret-2"},
		},
		HealthCheck: &HealthCheck{Port: 8080, Path: "/healthz?ready=1", TimeoutSeconds: 60},
		VolumeMounts: []VolumeMount{
			{VolumeID: "volume-1", ContainerPath: "/var/lib/a"},
			{VolumeID: "volume-2", ContainerPath: "/var/lib/z"},
		},
	})
	if err != nil || string(encoded) != string(secondEncoded) || hash != secondHash {
		t.Fatalf("canonical form is unstable: %v\n%s\n%s\n%s\n%s", err, encoded, secondEncoded, hash, secondHash)
	}
}

func TestSnapshotValidationRejectsUnsafeOrAmbiguousConfiguration(t *testing.T) {
	tests := []Snapshot{
		{Source: PublicImageSource("UPPERCASE/image:tag")},
		{Source: PublicImageSource("alpine"), Environment: map[string]string{"BAD-NAME": "value"}},
		{Source: PublicImageSource("alpine"), Environment: map[string]string{"PLATFORMD_SERVICE_ID": "override"}},
		{Source: PublicImageSource("alpine"), Environment: map[string]string{"TOKEN": "plain"}, SecretReferences: []SecretReference{{EnvironmentName: "TOKEN", SecretID: "secret"}}},
		{Source: PublicImageSource("alpine"), BuildEnvironment: map[string]string{"TOKEN": "secret"}},
		{Source: githubTestSource(), BuildEnvironment: map[string]string{"BAD-NAME": "secret"}},
		{Source: PublicImageSource("alpine"), HealthCheck: &HealthCheck{Path: "/healthz"}},
		{Source: PublicImageSource("alpine"), HealthCheck: &HealthCheck{Port: 8080, Path: "https://example.com/health"}},
		{Source: PublicImageSource("alpine"), VolumeMounts: []VolumeMount{{VolumeID: "volume", ContainerPath: "/"}}},
		{Source: PublicImageSource("alpine"), VolumeMounts: []VolumeMount{{VolumeID: "volume", ContainerPath: "/data"}, {VolumeID: "volume", ContainerPath: "/other"}}},
	}
	for index, input := range tests {
		if _, err := Normalize(input); err == nil {
			t.Fatalf("test %d accepted invalid snapshot: %+v", index, input)
		}
	}
}

func TestGitHubSnapshotNormalizesBuildEnvironment(t *testing.T) {
	normalized, err := Normalize(Snapshot{
		Source: githubTestSource(),
		BuildEnvironment: map[string]string{
			"DATABASE_URL": "${{database.DATABASE_URL}}",
			"SENTRY_TOKEN": "secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.BuildEnvironment["DATABASE_URL"] != "${{database.DATABASE_URL}}" || len(normalized.Environment) != 0 {
		t.Fatalf("normalized build environment = %#v", normalized.BuildEnvironment)
	}
}

func TestBeforeDeployNormalizesAllActionsAndArbitraryWorkflowInputs(t *testing.T) {
	normalized, err := Normalize(Snapshot{
		Source: githubTestSource(),
		BeforeDeploy: &BeforeDeploy{
			Command: "  bun run migrate  ",
			GitHubWorkflow: &GitHubWorkflow{
				Path: ".github/workflows/migrate.yml", Name: " Migrate database ",
				Inputs: map[string]any{"dryRun": false, "batch": float64(20), "metadata": map[string]any{"actor": "platformd"}},
			},
			CloudflareHostnames: []string{"WWW.Example.com", "api.example.com", "www.example.com"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeDeploy := normalized.BeforeDeploy
	if beforeDeploy == nil || beforeDeploy.Command != "bun run migrate" || beforeDeploy.GitHubWorkflow == nil || beforeDeploy.GitHubWorkflow.Name != "Migrate database" {
		t.Fatalf("normalized before deploy = %+v", beforeDeploy)
	}
	if got := strings.Join(beforeDeploy.CloudflareHostnames, ","); got != "api.example.com,www.example.com" {
		t.Fatalf("Cloudflare hostnames = %q", got)
	}
	if beforeDeploy.GitHubWorkflow.Inputs["dryRun"] != false || beforeDeploy.GitHubWorkflow.Inputs["batch"] != float64(20) {
		t.Fatalf("workflow inputs = %#v", beforeDeploy.GitHubWorkflow.Inputs)
	}
}

func TestBeforeDeployWorkflowRequiresGitHubSource(t *testing.T) {
	_, err := Normalize(Snapshot{
		Source: PublicImageSource("alpine"),
		BeforeDeploy: &BeforeDeploy{GitHubWorkflow: &GitHubWorkflow{
			Path: ".github/workflows/migrate.yml", Name: "Migrate", Inputs: map[string]any{},
		}},
	})
	if err == nil {
		t.Fatal("non-GitHub source accepted a before-deploy workflow")
	}
}

func githubTestSource() servicesource.Source {
	return servicesource.Source{
		Type: servicesource.GitHubImage,
		GitHub: &servicesource.GitHub{
			RepositoryID: 1, Repository: "acme/api", Branch: "main",
			DockerfilePath: "Dockerfile", ContextPath: ".",
		},
	}
}

func TestPinnedReferenceUsesRepositoryAndExactDigest(t *testing.T) {
	const digest = "sha256:5f70bf18a08660b3c3e431d73e3a1b13f1f4f9f365f22c4b155b87f12ee41a68"
	pinned, err := PinnedReference("alpine:3.22", digest)
	if err != nil {
		t.Fatal(err)
	}
	if pinned != "docker.io/library/alpine@"+digest || !IsDigestReference(pinned) || IsDigestReference("alpine:3.22") {
		t.Fatalf("pinned/digest detection = %q", pinned)
	}
}
