package servicesource

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/iivankin/platformd/internal/publichostname"
	"go.podman.io/image/v5/docker/reference"
)

const MaximumReleaseAgeDays = 36_500

var (
	uploadRepository = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?/[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$`)
	workflowName     = regexp.MustCompile(`^[^/\\\x00]+\.ya?ml$`)
)

func ValidUploadRepository(value string) bool {
	return uploadRepository.MatchString(value)
}

func ValidWorkflowName(value string) bool {
	return workflowName.MatchString(value)
}

type Kind string

const (
	DockerImageUpload Kind = "docker_image_upload"
	Unconfigured      Kind = "unconfigured"

	PublicImage  Kind = "public_image"
	PrivateImage Kind = "private_image"
)

type Image struct {
	Reference string `json:"reference"`
}

type DockerUpload struct {
	Repository    string   `json:"repository"`
	Branch        string   `json:"branch"`
	Workflows     []string `json:"workflows"`
	Previews      bool     `json:"previews"`
	PreviewDomain string   `json:"previewDomain,omitempty"`
}

type Source struct {
	Type                  Kind          `json:"type"`
	AutoUpdate            bool          `json:"autoUpdate,omitempty"`
	MinimumReleaseAgeDays int           `json:"minimumReleaseAgeDays,omitempty"`
	Image                 *Image        `json:"image,omitempty"`
	DockerUpload          *DockerUpload `json:"dockerUpload,omitempty"`
}

func Normalize(input Source) (Source, error) {
	source := input
	switch input.Type {
	case PublicImage, PrivateImage:
		if input.Image == nil || input.DockerUpload != nil {
			return Source{}, errors.New("image source must contain only image settings")
		}
		if input.MinimumReleaseAgeDays < 0 || input.MinimumReleaseAgeDays > MaximumReleaseAgeDays {
			return Source{}, fmt.Errorf("minimum release age must be between 0 and %d days", MaximumReleaseAgeDays)
		}
		parsed, err := reference.ParseDockerRef(strings.TrimSpace(input.Image.Reference))
		if err != nil {
			return Source{}, fmt.Errorf("invalid image reference: %w", err)
		}
		source.Image = &Image{Reference: parsed.String()}
	case DockerImageUpload:
		if input.DockerUpload == nil || input.Image != nil || input.AutoUpdate || input.MinimumReleaseAgeDays != 0 {
			return Source{}, errors.New("Docker image upload source must contain only upload settings")
		}
		upload := *input.DockerUpload
		upload.Repository = strings.ToLower(strings.TrimSpace(upload.Repository))
		upload.Branch = strings.TrimSpace(upload.Branch)
		if !uploadRepository.MatchString(upload.Repository) {
			return Source{}, errors.New("upload repository must be a lowercase owner/name")
		}
		if upload.Branch == "" || strings.HasPrefix(upload.Branch, "/") || strings.HasSuffix(upload.Branch, "/") ||
			strings.Contains(upload.Branch, "..") || strings.ContainsAny(upload.Branch, "~^:?*[\\\x00") {
			return Source{}, errors.New("upload production branch is invalid")
		}
		seen := make(map[string]struct{}, len(upload.Workflows))
		workflows := make([]string, 0, len(upload.Workflows))
		for _, value := range upload.Workflows {
			name := strings.TrimSpace(value)
			if !workflowName.MatchString(name) {
				return Source{}, errors.New("upload workflows must be simple .yml or .yaml filenames")
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			workflows = append(workflows, name)
		}
		upload.Workflows = workflows
		if upload.Previews {
			previewDomain, err := publichostname.NormalizeApex(upload.PreviewDomain)
			if err != nil {
				return Source{}, err
			}
			upload.PreviewDomain = previewDomain
		} else if strings.TrimSpace(upload.PreviewDomain) != "" {
			return Source{}, errors.New("preview domain is only valid when image previews are enabled")
		} else {
			upload.PreviewDomain = ""
		}
		source.DockerUpload = &upload
	case Unconfigured:
		if input.Image != nil || input.DockerUpload != nil || input.AutoUpdate || input.MinimumReleaseAgeDays != 0 {
			return Source{}, errors.New("unconfigured source cannot contain settings")
		}
	default:
		return Source{}, fmt.Errorf("unsupported service source type %q", input.Type)
	}
	return source, nil
}

func ImageReference(source Source) string {
	if source.Image == nil {
		return ""
	}
	return source.Image.Reference
}

func IsImage(source Source) bool {
	return source.Type == PublicImage || source.Type == PrivateImage
}

func IsRemoteImage(source Source) bool {
	return source.Type == PublicImage || source.Type == PrivateImage
}

// ImageUploadPreviewsEnabled reports whether non-latest image upload tags should
// publish ephemeral preview deployments under the configured preview root domain.
func ImageUploadPreviewsEnabled(source Source) bool {
	return source.Type == DockerImageUpload && source.DockerUpload != nil && source.DockerUpload.Previews
}

// ImageUploadPreviewDomain returns the configured apex preview domain, or empty.
func ImageUploadPreviewDomain(source Source) string {
	if !ImageUploadPreviewsEnabled(source) || source.DockerUpload == nil {
		return ""
	}
	return source.DockerUpload.PreviewDomain
}

// PreviewCoverageHostname is a single-label child of the preview root used to
// verify that Origin certificates cover preview deployments.
func PreviewCoverageHostname(previewRoot string) string {
	return "preview-coverage." + previewRoot
}
