package daemon

import (
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
)

func TestValidateImageUploadPreviewDomainAttach(t *testing.T) {
	t.Parallel()
	domains := []state.ServiceDomain{{Hostname: "kb.example.com"}}
	upload := func(previews bool) servicesource.Source {
		return servicesource.Source{
			Type: servicesource.DockerImageUpload,
			DockerUpload: &servicesource.DockerUpload{
				Repository: "acme/api", Branch: "main", Previews: previews,
			},
		}
	}
	cases := []struct {
		name     string
		source   servicesource.Source
		domains  []state.ServiceDomain
		hostname string
		wantErr  error
	}{
		{
			name:     "public image ignores count",
			source:   servicesource.Source{Type: servicesource.PublicImage, Image: &servicesource.Image{Reference: "alpine"}},
			hostname: "app.example.com",
		},
		{
			name:     "upload without previews ignores domains",
			source:   upload(false),
			domains:  domains,
			hostname: "other.example.com",
		},
		{
			name:     "upload previews allow first domain",
			source:   upload(true),
			hostname: "kb.example.com",
		},
		{
			name:     "upload previews allow same hostname update",
			source:   upload(true),
			domains:  domains,
			hostname: "kb.example.com",
		},
		{
			name:     "upload previews reject second domain",
			source:   upload(true),
			domains:  domains,
			hostname: "other.example.com",
			wantErr:  state.ErrPreviewDomainCount,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			err := validateImageUploadPreviewDomainAttach(testCase.source, testCase.domains, testCase.hostname)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("err = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}
