package serviceconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/opencontainers/go-digest"
	"go.podman.io/image/v5/docker/reference"
)

const (
	DefaultHealthTimeoutSeconds = 60
	maximumBeforeDeployBytes    = 256 << 10
	maximumCloudflareHostnames  = 30
	maximumEnvironmentBytes     = 256 << 10
	maximumEnvironmentVariables = 1024
	maximumProcessArguments     = 1024
	maximumProcessBytes         = 256 << 10
	maximumWorkflowInputs       = 25
)

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type SecretReference struct {
	EnvironmentName string `json:"environmentName"`
	SecretID        string `json:"secretId"`
}

type VolumeMount struct {
	VolumeID      string `json:"volumeId"`
	ContainerPath string `json:"containerPath"`
}

type HealthCheck struct {
	Port           int    `json:"port"`
	Path           string `json:"path"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

type GitHubWorkflow struct {
	Path   string         `json:"path"`
	Name   string         `json:"name"`
	Inputs map[string]any `json:"inputs"`
}

type BeforeDeploy struct {
	Command             string          `json:"command,omitempty"`
	GitHubWorkflow      *GitHubWorkflow `json:"githubWorkflow,omitempty"`
	CloudflareHostnames []string        `json:"cloudflareHostnames"`
}

type Snapshot struct {
	Source           servicesource.Source `json:"source"`
	BeforeDeploy     *BeforeDeploy        `json:"beforeDeploy,omitempty"`
	Command          []string             `json:"command,omitempty"`
	Args             []string             `json:"args,omitempty"`
	Environment      map[string]string    `json:"environment"`
	BuildEnvironment map[string]string    `json:"buildEnvironment"`
	SecretReferences []SecretReference    `json:"secretReferences"`
	HealthCheck      *HealthCheck         `json:"healthCheck,omitempty"`
	CPUMillicores    int64                `json:"cpuMillicores,omitempty"`
	MemoryMaxBytes   int64                `json:"memoryMaxBytes,omitempty"`
	VolumeMounts     []VolumeMount        `json:"volumeMounts"`
}

// Source helpers keep programmatic callers on the same explicit source model
// as JSON clients without duplicating source literals throughout the daemon.
func PlatformRegistrySource(reference string) servicesource.Source {
	return servicesource.Source{
		Type: servicesource.RegistryImage, AutoUpdate: true,
		Image: &servicesource.Image{Reference: reference},
	}
}

func PublicImageSource(reference string) servicesource.Source {
	return servicesource.Source{
		Type: servicesource.PublicImage, AutoUpdate: true,
		Image: &servicesource.Image{Reference: reference},
	}
}

func PrivateImageSource(reference string) servicesource.Source {
	return servicesource.Source{
		Type: servicesource.PrivateImage, AutoUpdate: true,
		Image: &servicesource.Image{Reference: reference},
	}
}

func Normalize(input Snapshot) (Snapshot, error) {
	normalized := input
	source, err := servicesource.Normalize(input.Source)
	if err != nil {
		return Snapshot{}, err
	}
	normalized.Source = source
	beforeDeploy, err := normalizeBeforeDeploy(input.BeforeDeploy, source)
	if err != nil {
		return Snapshot{}, err
	}
	normalized.BeforeDeploy = beforeDeploy
	if input.HealthCheck != nil {
		healthCheck := *input.HealthCheck
		if healthCheck.TimeoutSeconds == 0 {
			healthCheck.TimeoutSeconds = DefaultHealthTimeoutSeconds
		}
		normalized.HealthCheck = &healthCheck
	}
	if err := validateSnapshot(normalized); err != nil {
		return Snapshot{}, err
	}

	normalized.Command = cloneSlice(input.Command)
	normalized.Args = cloneSlice(input.Args)
	normalized.Environment = cloneMap(input.Environment)
	normalized.BuildEnvironment = cloneMap(input.BuildEnvironment)
	normalized.SecretReferences = append([]SecretReference(nil), input.SecretReferences...)
	normalized.VolumeMounts = append([]VolumeMount(nil), input.VolumeMounts...)
	sort.Slice(normalized.SecretReferences, func(left, right int) bool {
		if normalized.SecretReferences[left].EnvironmentName == normalized.SecretReferences[right].EnvironmentName {
			return normalized.SecretReferences[left].SecretID < normalized.SecretReferences[right].SecretID
		}
		return normalized.SecretReferences[left].EnvironmentName < normalized.SecretReferences[right].EnvironmentName
	})
	sort.Slice(normalized.VolumeMounts, func(left, right int) bool {
		if normalized.VolumeMounts[left].ContainerPath == normalized.VolumeMounts[right].ContainerPath {
			return normalized.VolumeMounts[left].VolumeID < normalized.VolumeMounts[right].VolumeID
		}
		return normalized.VolumeMounts[left].ContainerPath < normalized.VolumeMounts[right].ContainerPath
	})
	if normalized.Environment == nil {
		normalized.Environment = make(map[string]string)
	}
	if normalized.BuildEnvironment == nil {
		normalized.BuildEnvironment = make(map[string]string)
	}
	if normalized.SecretReferences == nil {
		normalized.SecretReferences = make([]SecretReference, 0)
	}
	if normalized.VolumeMounts == nil {
		normalized.VolumeMounts = make([]VolumeMount, 0)
	}
	return normalized, nil
}

func Canonical(input Snapshot) (Snapshot, []byte, string, error) {
	normalized, err := Normalize(input)
	if err != nil {
		return Snapshot{}, nil, "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return Snapshot{}, nil, "", fmt.Errorf("encode service snapshot: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return normalized, encoded, hex.EncodeToString(hash[:]), nil
}

func PinnedReference(imageReference, imageDigest string) (string, error) {
	named, err := reference.ParseDockerRef(strings.TrimSpace(imageReference))
	if err != nil {
		return "", fmt.Errorf("invalid image reference: %w", err)
	}
	parsedDigest, err := digest.Parse(imageDigest)
	if err != nil {
		return "", fmt.Errorf("invalid image digest: %w", err)
	}
	repository, err := reference.WithName(named.Name())
	if err != nil {
		return "", fmt.Errorf("build image repository reference: %w", err)
	}
	pinned, err := reference.WithDigest(repository, parsedDigest)
	if err != nil {
		return "", fmt.Errorf("pin image digest: %w", err)
	}
	return pinned.String(), nil
}

func IsDigestReference(imageReference string) bool {
	named, err := reference.ParseDockerRef(strings.TrimSpace(imageReference))
	if err != nil {
		return false
	}
	_, ok := named.(reference.Digested)
	return ok
}

func validateSnapshot(snapshot Snapshot) error {
	if err := validateProcess(snapshot.Command, snapshot.Args); err != nil {
		return err
	}
	if err := validateEnvironment(snapshot.Environment, snapshot.SecretReferences); err != nil {
		return err
	}
	if len(snapshot.BuildEnvironment) > 0 && snapshot.Source.Type != servicesource.GitHubImage {
		return errors.New("build environment is only valid for GitHub sources")
	}
	if err := validateEnvironment(snapshot.BuildEnvironment, nil); err != nil {
		return fmt.Errorf("build environment: %w", err)
	}
	if len(snapshot.Environment)+len(snapshot.SecretReferences)+len(snapshot.BuildEnvironment) > maximumEnvironmentVariables {
		return errors.New("service contains too many variables")
	}
	if environmentBytes(snapshot.Environment, snapshot.SecretReferences)+environmentBytes(snapshot.BuildEnvironment, nil) > maximumEnvironmentBytes {
		return errors.New("service variables exceed 256 KiB")
	}
	if snapshot.HealthCheck != nil {
		if snapshot.HealthCheck.Port < 1 || snapshot.HealthCheck.Port > 65535 {
			return errors.New("health check port must be between 1 and 65535")
		}
		parsed, err := url.ParseRequestURI(snapshot.HealthCheck.Path)
		if err != nil || !strings.HasPrefix(snapshot.HealthCheck.Path, "/") || parsed.Host != "" || parsed.Fragment != "" {
			return errors.New("health path must be an absolute HTTP request path")
		}
		if snapshot.HealthCheck.TimeoutSeconds < 1 || snapshot.HealthCheck.TimeoutSeconds > 3600 {
			return errors.New("health check timeout must be between 1 and 3600 seconds")
		}
	}
	if snapshot.CPUMillicores < 0 || snapshot.MemoryMaxBytes < 0 {
		return errors.New("resource limits cannot be negative")
	}
	return validateVolumeMounts(snapshot.VolumeMounts)
}

func normalizeBeforeDeploy(input *BeforeDeploy, source servicesource.Source) (*BeforeDeploy, error) {
	if input == nil {
		return nil, nil
	}
	result := &BeforeDeploy{
		Command:             strings.TrimSpace(input.Command),
		CloudflareHostnames: make([]string, 0, len(input.CloudflareHostnames)),
	}
	if strings.ContainsRune(result.Command, '\x00') || len(result.Command) > maximumBeforeDeployBytes {
		return nil, errors.New("before-deploy command is invalid or exceeds 256 KiB")
	}
	seenHostnames := make(map[string]struct{}, len(input.CloudflareHostnames))
	for _, value := range input.CloudflareHostnames {
		hostname, err := publichostname.Normalize(value)
		if err != nil {
			return nil, fmt.Errorf("invalid before-deploy Cloudflare hostname: %w", err)
		}
		if _, exists := seenHostnames[hostname]; exists {
			continue
		}
		seenHostnames[hostname] = struct{}{}
		result.CloudflareHostnames = append(result.CloudflareHostnames, hostname)
	}
	if len(result.CloudflareHostnames) > maximumCloudflareHostnames {
		return nil, fmt.Errorf("before-deploy Cloudflare purge supports at most %d hostnames", maximumCloudflareHostnames)
	}
	sort.Strings(result.CloudflareHostnames)
	if input.GitHubWorkflow != nil {
		if source.Type != servicesource.GitHubImage {
			return nil, errors.New("before-deploy GitHub workflow requires a GitHub source")
		}
		workflow := &GitHubWorkflow{
			Path: strings.TrimSpace(input.GitHubWorkflow.Path),
			Name: strings.TrimSpace(input.GitHubWorkflow.Name),
		}
		if !validWorkflowPath(workflow.Path) || workflow.Name == "" || len(workflow.Name) > 512 || strings.ContainsRune(workflow.Name, '\x00') {
			return nil, errors.New("before-deploy GitHub workflow is invalid")
		}
		if len(input.GitHubWorkflow.Inputs) > maximumWorkflowInputs {
			return nil, fmt.Errorf("before-deploy GitHub workflow supports at most %d inputs", maximumWorkflowInputs)
		}
		encoded, err := json.Marshal(input.GitHubWorkflow.Inputs)
		if err != nil || len(encoded) > maximumBeforeDeployBytes {
			return nil, errors.New("before-deploy GitHub inputs are invalid or exceed 256 KiB")
		}
		if input.GitHubWorkflow.Inputs == nil {
			workflow.Inputs = make(map[string]any)
		} else if err := json.Unmarshal(encoded, &workflow.Inputs); err != nil {
			return nil, errors.New("before-deploy GitHub inputs are invalid")
		}
		result.GitHubWorkflow = workflow
	}
	if result.Command == "" && result.GitHubWorkflow == nil && len(result.CloudflareHostnames) == 0 {
		return nil, nil
	}
	return result, nil
}

func validWorkflowPath(value string) bool {
	if !strings.HasPrefix(value, ".github/workflows/") || path.Clean(value) != value {
		return false
	}
	name := strings.TrimPrefix(value, ".github/workflows/")
	return name != "" && !strings.Contains(name, "/") &&
		(strings.HasSuffix(strings.ToLower(name), ".yml") || strings.HasSuffix(strings.ToLower(name), ".yaml"))
}

func validateProcess(command, arguments []string) error {
	if len(command) > maximumProcessArguments || len(arguments) > maximumProcessArguments {
		return errors.New("command or args contains too many entries")
	}
	bytes := 0
	for _, value := range append(append([]string(nil), command...), arguments...) {
		if strings.ContainsRune(value, '\x00') {
			return errors.New("command and args cannot contain NUL")
		}
		bytes += len(value)
	}
	if bytes > maximumProcessBytes {
		return errors.New("command and args exceed 256 KiB")
	}
	return nil
}

func validateEnvironment(environment map[string]string, secretReferences []SecretReference) error {
	if len(environment)+len(secretReferences) > maximumEnvironmentVariables {
		return errors.New("environment contains too many variables")
	}
	seen := make(map[string]struct{}, len(environment)+len(secretReferences))
	for name, value := range environment {
		if !environmentName.MatchString(name) {
			return fmt.Errorf("invalid environment name %q", name)
		}
		if strings.HasPrefix(name, "PLATFORMD_") {
			return fmt.Errorf("environment name %q is reserved by platformd", name)
		}
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("environment %s contains NUL", name)
		}
		seen[name] = struct{}{}
	}
	for _, reference := range secretReferences {
		if !environmentName.MatchString(reference.EnvironmentName) || reference.SecretID == "" || strings.ContainsRune(reference.SecretID, '\x00') {
			return errors.New("invalid secret environment reference")
		}
		if strings.HasPrefix(reference.EnvironmentName, "PLATFORMD_") {
			return fmt.Errorf("environment name %q is reserved by platformd", reference.EnvironmentName)
		}
		if _, exists := seen[reference.EnvironmentName]; exists {
			return fmt.Errorf("duplicate environment name %q", reference.EnvironmentName)
		}
		seen[reference.EnvironmentName] = struct{}{}
	}
	if environmentBytes(environment, secretReferences) > maximumEnvironmentBytes {
		return errors.New("environment exceeds 256 KiB")
	}
	return nil
}

func environmentBytes(environment map[string]string, secretReferences []SecretReference) int {
	bytes := 0
	for name, value := range environment {
		bytes += len(name) + len(value)
	}
	for _, reference := range secretReferences {
		bytes += len(reference.EnvironmentName) + len(reference.SecretID)
	}
	return bytes
}

func validateVolumeMounts(mounts []VolumeMount) error {
	volumeIDs := make(map[string]struct{}, len(mounts))
	paths := make(map[string]struct{}, len(mounts))
	for _, mount := range mounts {
		if mount.VolumeID == "" || strings.ContainsRune(mount.VolumeID, '\x00') {
			return errors.New("volume mount requires a valid volume ID")
		}
		if !strings.HasPrefix(mount.ContainerPath, "/") || mount.ContainerPath == "/" || path.Clean(mount.ContainerPath) != mount.ContainerPath {
			return fmt.Errorf("invalid volume container path %q", mount.ContainerPath)
		}
		if _, exists := volumeIDs[mount.VolumeID]; exists {
			return fmt.Errorf("volume %s is mounted more than once", mount.VolumeID)
		}
		if _, exists := paths[mount.ContainerPath]; exists {
			return fmt.Errorf("duplicate volume container path %s", mount.ContainerPath)
		}
		volumeIDs[mount.VolumeID] = struct{}{}
		paths[mount.ContainerPath] = struct{}{}
	}
	return nil
}

func cloneSlice(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
