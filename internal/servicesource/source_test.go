package servicesource

import "testing"

func TestNormalizeImageSourceKinds(t *testing.T) {
	tests := []struct {
		input   Source
		kind    Kind
		want    string
		wantAge int
	}{
		{
			input:   Source{Type: PublicImage, AutoUpdate: true, MinimumReleaseAgeDays: 7, Image: &Image{Reference: "alpine:3.22"}},
			kind:    PublicImage,
			want:    "docker.io/library/alpine:3.22",
			wantAge: 7,
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
		if normalized.Type != test.kind || normalized.Image == nil || normalized.Image.Reference != test.want ||
			normalized.MinimumReleaseAgeDays != test.wantAge {
			t.Fatalf("normalized %s source = %+v", test.kind, normalized)
		}
	}
}

func TestNormalizeDockerImageUpload(t *testing.T) {
	normalized, err := Normalize(Source{Type: DockerImageUpload, DockerUpload: &DockerUpload{
		Repository: " acme/backend ", Branch: "main", Workflows: []string{"deploy.yml", "deploy.yml", "release.yaml"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.DockerUpload == nil || normalized.DockerUpload.Repository != "acme/backend" ||
		len(normalized.DockerUpload.Workflows) != 2 {
		t.Fatalf("normalized upload source = %+v", normalized)
	}
	for _, invalid := range []Source{
		{Type: DockerImageUpload, DockerUpload: &DockerUpload{Repository: "backend", Branch: "main"}},
		{Type: DockerImageUpload, DockerUpload: &DockerUpload{Repository: "acme/backend", Branch: "../main"}},
		{Type: DockerImageUpload, DockerUpload: &DockerUpload{Repository: "acme/backend", Branch: "main", Workflows: []string{"nested/deploy.yml"}}},
	} {
		if _, err := Normalize(invalid); err == nil {
			t.Fatalf("accepted invalid upload source: %+v", invalid)
		}
	}
}

func TestNormalizeMinimumReleaseAgeOnlyAllowsRemoteImages(t *testing.T) {
	for _, source := range []Source{
		{Type: PublicImage, MinimumReleaseAgeDays: -1, Image: &Image{Reference: "alpine:latest"}},
		{Type: PrivateImage, MinimumReleaseAgeDays: MaximumReleaseAgeDays + 1, Image: &Image{Reference: "ghcr.io/acme/api:latest"}},
		{Type: DockerImageUpload, MinimumReleaseAgeDays: 7, DockerUpload: &DockerUpload{Repository: "acme/api", Branch: "main"}},
	} {
		if _, err := Normalize(source); err == nil {
			t.Fatalf("normalized invalid source: %+v", source)
		}
	}
}
