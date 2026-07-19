package servicesource

import "testing"

func TestNormalizeImageSourceKinds(t *testing.T) {
	tests := []struct {
		input Source
		kind  Kind
		want  string
	}{
		{
			input: Source{Type: RegistryImage, AutoUpdate: true, Image: &Image{Reference: "registry.example.com/team/api:latest"}},
			kind:  RegistryImage,
			want:  "registry.example.com/team/api:latest",
		},
		{
			input: Source{Type: PublicImage, AutoUpdate: true, Image: &Image{Reference: "alpine:3.22"}},
			kind:  PublicImage,
			want:  "docker.io/library/alpine:3.22",
		},
		{
			input: Source{Type: PrivateImage, Image: &Image{Reference: "ghcr.io/acme/api:latest"}},
			kind:  PrivateImage,
			want:  "ghcr.io/acme/api:latest",
		},
	}
	for _, test := range tests {
		normalized, err := Normalize(test.input)
		if err != nil {
			t.Fatalf("normalize %s: %v", test.kind, err)
		}
		if normalized.Type != test.kind || normalized.Image == nil || normalized.Image.Reference != test.want {
			t.Fatalf("normalized %s source = %+v", test.kind, normalized)
		}
	}
}

func TestNormalizeGitHubSourceKeepsBuildPolicy(t *testing.T) {
	normalized, err := Normalize(Source{
		Type: GitHubImage, AutoUpdate: true,
		GitHub: &GitHub{
			RepositoryID: 42, Repository: " owner/repository ", Branch: " main ",
			DockerfilePath: "/deploy/Dockerfile", ContextPath: "/.",
			TriggerPaths: []string{"apps/api/", "apps/api", "../unsafe"}, WaitForCI: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.AutoUpdate || normalized.GitHub == nil || !normalized.GitHub.WaitForCI ||
		normalized.GitHub.Repository != "owner/repository" || normalized.GitHub.Branch != "main" ||
		normalized.GitHub.DockerfilePath != "deploy/Dockerfile" || normalized.GitHub.ContextPath != "." ||
		len(normalized.GitHub.TriggerPaths) != 1 || normalized.GitHub.TriggerPaths[0] != "apps/api" {
		t.Fatalf("normalized GitHub source = %+v", normalized)
	}
}

func TestNormalizeGitHubSourceValidatesPullRequestPreviewTemplate(t *testing.T) {
	source, err := Normalize(Source{Type: GitHubImage, GitHub: &GitHub{
		RepositoryID: 1, Repository: "owner/repository", Branch: "main",
		DockerfilePath: "Dockerfile", ContextPath: ".",
		PullRequestPreview: &PullRequestPreview{HostnameTemplate: " Preview-{{HASH}}.Example.COM "},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if source.GitHub.PullRequestPreview.HostnameTemplate != "preview-{{hash}}.example.com" {
		t.Fatalf("template = %q", source.GitHub.PullRequestPreview.HostnameTemplate)
	}
	hostname, err := PreviewHostname(source.GitHub.PullRequestPreview.HostnameTemplate, "0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	if hostname != "preview-0123456789ab.example.com" {
		t.Fatalf("hostname = %q", hostname)
	}
}

func TestNormalizeGitHubSourceRejectsInvalidPullRequestPreviewTemplate(t *testing.T) {
	_, err := Normalize(Source{Type: GitHubImage, GitHub: &GitHub{
		RepositoryID: 1, Repository: "owner/repository", Branch: "main",
		DockerfilePath: "Dockerfile", ContextPath: ".",
		PullRequestPreview: &PullRequestPreview{HostnameTemplate: "preview.example.com"},
	}})
	if err == nil {
		t.Fatal("expected invalid preview template")
	}
}
