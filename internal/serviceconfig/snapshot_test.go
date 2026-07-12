package serviceconfig

import (
	"strings"
	"testing"
)

func TestCanonicalNormalizesAndHashesServiceSnapshot(t *testing.T) {
	port := 8080
	input := Snapshot{
		ImageReference: "alpine",
		Environment:    map[string]string{"DATABASE_URL": "postgres://db:5432/app"},
		SecretReferences: []SecretReference{
			{EnvironmentName: "TOKEN", SecretID: "secret-2"},
			{EnvironmentName: "PASSWORD", SecretID: "secret-1"},
		},
		TargetPort: &port,
		HealthPath: "/healthz?ready=1",
		VolumeMounts: []VolumeMount{
			{VolumeID: "volume-2", ContainerPath: "/var/lib/z"},
			{VolumeID: "volume-1", ContainerPath: "/var/lib/a"},
		},
	}
	normalized, encoded, hash, err := Canonical(input)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.ImageReference != "docker.io/library/alpine:latest" || normalized.StartupTimeoutSeconds != 60 {
		t.Fatalf("normalized snapshot = %+v", normalized)
	}
	if len(hash) != 64 || !strings.Contains(string(encoded), `"containerPath":"/var/lib/a"`) {
		t.Fatalf("encoded/hash = %s/%s", encoded, hash)
	}
	_, secondEncoded, secondHash, err := Canonical(Snapshot{
		ImageReference: "docker.io/library/alpine:latest",
		Environment:    map[string]string{"DATABASE_URL": "postgres://db:5432/app"},
		SecretReferences: []SecretReference{
			{EnvironmentName: "PASSWORD", SecretID: "secret-1"},
			{EnvironmentName: "TOKEN", SecretID: "secret-2"},
		},
		TargetPort: &port, HealthPath: "/healthz?ready=1", StartupTimeoutSeconds: 60,
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
	port := 8080
	tests := []Snapshot{
		{ImageReference: "UPPERCASE/image:tag"},
		{ImageReference: "alpine", Environment: map[string]string{"BAD-NAME": "value"}},
		{ImageReference: "alpine", Environment: map[string]string{"TOKEN": "plain"}, SecretReferences: []SecretReference{{EnvironmentName: "TOKEN", SecretID: "secret"}}},
		{ImageReference: "alpine", HealthPath: "/healthz"},
		{ImageReference: "alpine", TargetPort: &port, HealthPath: "https://example.com/health"},
		{ImageReference: "alpine", VolumeMounts: []VolumeMount{{VolumeID: "volume", ContainerPath: "/"}}},
		{ImageReference: "alpine", VolumeMounts: []VolumeMount{{VolumeID: "volume", ContainerPath: "/data"}, {VolumeID: "volume", ContainerPath: "/other"}}},
	}
	for index, input := range tests {
		if _, err := Normalize(input); err == nil {
			t.Fatalf("test %d accepted invalid snapshot: %+v", index, input)
		}
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
