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

	"github.com/opencontainers/go-digest"
	"go.podman.io/image/v5/docker/reference"
)

const (
	DefaultStartupTimeoutSeconds = 60
	maximumEnvironmentBytes      = 256 << 10
	maximumEnvironmentVariables  = 1024
	maximumProcessArguments      = 1024
	maximumProcessBytes          = 256 << 10
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

type Snapshot struct {
	ImageReference        string            `json:"imageReference"`
	RegistryCredentialID  string            `json:"registryCredentialId,omitempty"`
	Command               []string          `json:"command,omitempty"`
	Args                  []string          `json:"args,omitempty"`
	Environment           map[string]string `json:"environment"`
	SecretReferences      []SecretReference `json:"secretReferences"`
	TargetPort            *int              `json:"targetPort,omitempty"`
	HealthPath            string            `json:"healthPath,omitempty"`
	StartupTimeoutSeconds int               `json:"startupTimeoutSeconds"`
	CPUMillicores         int64             `json:"cpuMillicores,omitempty"`
	MemoryMaxBytes        int64             `json:"memoryMaxBytes,omitempty"`
	VolumeMounts          []VolumeMount     `json:"volumeMounts"`
}

func Normalize(input Snapshot) (Snapshot, error) {
	normalized := input
	image, err := reference.ParseDockerRef(strings.TrimSpace(input.ImageReference))
	if err != nil {
		return Snapshot{}, fmt.Errorf("invalid image reference: %w", err)
	}
	normalized.ImageReference = image.String()
	if normalized.StartupTimeoutSeconds == 0 {
		normalized.StartupTimeoutSeconds = DefaultStartupTimeoutSeconds
	}
	if err := validateSnapshot(normalized); err != nil {
		return Snapshot{}, err
	}

	normalized.Command = cloneSlice(input.Command)
	normalized.Args = cloneSlice(input.Args)
	if input.TargetPort != nil {
		targetPort := *input.TargetPort
		normalized.TargetPort = &targetPort
	}
	normalized.Environment = cloneMap(input.Environment)
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
	if snapshot.RegistryCredentialID != "" && strings.ContainsRune(snapshot.RegistryCredentialID, '\x00') {
		return errors.New("registry credential ID contains NUL")
	}
	if err := validateProcess(snapshot.Command, snapshot.Args); err != nil {
		return err
	}
	if err := validateEnvironment(snapshot.Environment, snapshot.SecretReferences); err != nil {
		return err
	}
	if snapshot.TargetPort != nil && (*snapshot.TargetPort < 1 || *snapshot.TargetPort > 65535) {
		return errors.New("target port must be between 1 and 65535")
	}
	if snapshot.HealthPath != "" {
		if snapshot.TargetPort == nil {
			return errors.New("health path requires target port")
		}
		parsed, err := url.ParseRequestURI(snapshot.HealthPath)
		if err != nil || !strings.HasPrefix(snapshot.HealthPath, "/") || parsed.Host != "" || parsed.Fragment != "" {
			return errors.New("health path must be an absolute HTTP request path")
		}
	}
	if snapshot.StartupTimeoutSeconds < 1 || snapshot.StartupTimeoutSeconds > 3600 {
		return errors.New("startup timeout must be between 1 and 3600 seconds")
	}
	if snapshot.CPUMillicores < 0 || snapshot.MemoryMaxBytes < 0 {
		return errors.New("resource limits cannot be negative")
	}
	return validateVolumeMounts(snapshot.VolumeMounts)
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
	bytes := 0
	for name, value := range environment {
		if !environmentName.MatchString(name) {
			return fmt.Errorf("invalid environment name %q", name)
		}
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("environment %s contains NUL", name)
		}
		seen[name] = struct{}{}
		bytes += len(name) + len(value)
	}
	for _, reference := range secretReferences {
		if !environmentName.MatchString(reference.EnvironmentName) || reference.SecretID == "" || strings.ContainsRune(reference.SecretID, '\x00') {
			return errors.New("invalid secret environment reference")
		}
		if _, exists := seen[reference.EnvironmentName]; exists {
			return fmt.Errorf("duplicate environment name %q", reference.EnvironmentName)
		}
		seen[reference.EnvironmentName] = struct{}{}
		bytes += len(reference.EnvironmentName) + len(reference.SecretID)
	}
	if bytes > maximumEnvironmentBytes {
		return errors.New("environment exceeds 256 KiB")
	}
	return nil
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
