package servicesource

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/iivankin/platformd/internal/publichostname"
	"go.podman.io/image/v5/docker/reference"
)

const (
	PreviewHashToken  = "{{hash}}"
	PreviewHashLength = 12
)

type Kind string

const (
	GitHubImage   Kind = "github"
	RegistryImage Kind = "platformd_registry"
	PublicImage   Kind = "public_image"
	PrivateImage  Kind = "private_image"
)

type Image struct {
	Reference string `json:"reference"`
}

type GitHub struct {
	RepositoryID       int64               `json:"repositoryId"`
	Repository         string              `json:"repository"`
	Branch             string              `json:"branch"`
	DockerfilePath     string              `json:"dockerfilePath"`
	ContextPath        string              `json:"contextPath"`
	TriggerPaths       []string            `json:"triggerPaths"`
	WaitForCI          bool                `json:"waitForCi"`
	Revision           string              `json:"revision,omitempty"`
	PullRequestPreview *PullRequestPreview `json:"pullRequestPreview,omitempty"`
}

type PullRequestPreview struct {
	HostnameTemplate string `json:"hostnameTemplate"`
}

type Source struct {
	Type       Kind    `json:"type"`
	AutoUpdate bool    `json:"autoUpdate,omitempty"`
	Image      *Image  `json:"image,omitempty"`
	GitHub     *GitHub `json:"github,omitempty"`
}

func Normalize(input Source) (Source, error) {
	source := input
	switch input.Type {
	case RegistryImage, PublicImage, PrivateImage:
		if input.Image == nil || input.GitHub != nil {
			return Source{}, errors.New("image source must contain only image settings")
		}
		parsed, err := reference.ParseDockerRef(strings.TrimSpace(input.Image.Reference))
		if err != nil {
			return Source{}, fmt.Errorf("invalid image reference: %w", err)
		}
		source.Image = &Image{Reference: parsed.String()}
	case GitHubImage:
		if input.GitHub == nil || input.Image != nil {
			return Source{}, errors.New("GitHub source must contain only GitHub settings")
		}
		github := *input.GitHub
		github.Repository = strings.TrimSpace(github.Repository)
		github.Branch = strings.TrimSpace(github.Branch)
		github.Revision = strings.TrimSpace(github.Revision)
		if github.Revision != "" && (len(github.Revision) != 40 || strings.IndexFunc(github.Revision, func(value rune) bool {
			return (value < '0' || value > '9') && (value < 'a' || value > 'f')
		}) != -1) {
			return Source{}, errors.New("GitHub revision must be a full lowercase commit SHA")
		}
		github.DockerfilePath = cleanRelativePath(github.DockerfilePath, "Dockerfile")
		github.ContextPath = cleanRelativePath(github.ContextPath, ".")
		if github.RepositoryID <= 0 || github.Repository == "" || github.Branch == "" {
			return Source{}, errors.New("GitHub repository and branch are required")
		}
		if github.DockerfilePath == "" || github.ContextPath == "" {
			return Source{}, errors.New("GitHub build paths must stay inside the repository")
		}
		github.TriggerPaths = normalizeTriggerPaths(github.TriggerPaths)
		if github.PullRequestPreview != nil {
			preview := *github.PullRequestPreview
			hostnameTemplate, err := NormalizePreviewHostnameTemplate(preview.HostnameTemplate)
			if err != nil {
				return Source{}, err
			}
			preview.HostnameTemplate = hostnameTemplate
			github.PullRequestPreview = &preview
		}
		source.AutoUpdate = false
		source.GitHub = &github
	default:
		return Source{}, fmt.Errorf("unsupported service source type %q", input.Type)
	}
	return source, nil
}

func NormalizePreviewHostnameTemplate(value string) (string, error) {
	template := strings.ToLower(strings.TrimSpace(value))
	if strings.Count(template, PreviewHashToken) != 1 {
		return "", fmt.Errorf("PR preview hostname must contain %s exactly once", PreviewHashToken)
	}
	if strings.Contains(strings.Replace(template, PreviewHashToken, "", 1), "{") ||
		strings.Contains(strings.Replace(template, PreviewHashToken, "", 1), "}") {
		return "", errors.New("PR preview hostname contains an unsupported template expression")
	}
	resolved := strings.Replace(template, PreviewHashToken, strings.Repeat("a", PreviewHashLength), 1)
	if _, err := publichostname.Normalize(resolved); err != nil {
		return "", fmt.Errorf("invalid PR preview hostname template: %w", err)
	}
	return template, nil
}

func PreviewHostname(template, revision string) (string, error) {
	normalized, err := NormalizePreviewHostnameTemplate(template)
	if err != nil {
		return "", err
	}
	if len(revision) < PreviewHashLength || strings.IndexFunc(revision, func(value rune) bool {
		return (value < '0' || value > '9') && (value < 'a' || value > 'f')
	}) != -1 {
		return "", errors.New("PR preview revision must be a lowercase commit SHA")
	}
	return publichostname.Normalize(strings.Replace(normalized, PreviewHashToken, revision[:PreviewHashLength], 1))
}

func ImageReference(source Source) string {
	if source.Image == nil {
		return ""
	}
	return source.Image.Reference
}

func IsImage(source Source) bool {
	return source.Type == RegistryImage || source.Type == PublicImage || source.Type == PrivateImage
}

func cleanRelativePath(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		trimmed = fallback
	}
	cleaned := path.Clean(strings.TrimPrefix(trimmed, "/"))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return cleaned
}

func normalizeTriggerPaths(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		cleaned := cleanRelativePath(value, "")
		if cleaned == "" || cleaned == "." {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		result = append(result, cleaned)
	}
	return result
}
