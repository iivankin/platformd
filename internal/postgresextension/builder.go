package postgresextension

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/systemevent"
)

const buildScript = `set -eu
case "${PG_MAJOR:-}" in
  ''|*[!0-9]*) echo "PG_MAJOR is unavailable" >&2; exit 65 ;;
esac
case "$(uname -s):$(test -f /etc/alpine-release && echo alpine || true):$(test -f /etc/debian_version && echo debian || true)" in
  Linux:alpine:)
    platformd_family=alpine
    apk add --no-cache --virtual .platformd-pgvector-build build-base
    ;;
  Linux::debian)
    platformd_family=debian
    printf 'Acquire::ForceIPv4 "true";\n' >/etc/apt/apt.conf.d/99platformd-network
    apt-get update
    apt-mark hold locales >/dev/null 2>&1 || true
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends build-essential "postgresql-server-dev-$PG_MAJOR"
    ;;
  *)
    echo "runtime extension builds require an Alpine- or Debian-based official PostgreSQL image" >&2
    exit 65
    ;;
esac
if [ ! -f "$(pg_config --includedir-server)/postgres.h" ]; then
  echo "official PostgreSQL server headers are unavailable" >&2
  exit 65
fi
rm -rf /tmp/platformd-pgvector
mkdir -p /tmp/platformd-pgvector
tar -xzf /platformd/vector.tar.gz --strip-components=1 -C /tmp/platformd-pgvector
cd /tmp/platformd-pgvector
make clean
case "$platformd_family" in
  alpine)
    # Official Alpine images retain server headers but not the large Clang toolchain.
    # PostgreSQL extensions do not require LLVM bitcode to load or execute.
    make OPTFLAGS="" with_llvm=no
    make install with_llvm=no
    ;;
  debian)
    make OPTFLAGS=""
    make install
    ;;
esac
mkdir -p /usr/share/doc/pgvector
cp LICENSE README.md /usr/share/doc/pgvector/
cd /
rm -rf /tmp/platformd-pgvector
case "$platformd_family" in
  alpine)
    apk del --no-cache .platformd-pgvector-build
    rm -rf /var/cache/apk/*
    ;;
  debian)
    DEBIAN_FRONTEND=noninteractive apt-get remove -y build-essential "postgresql-server-dev-$PG_MAJOR"
    DEBIAN_FRONTEND=noninteractive apt-get autoremove -y
    apt-mark unhold locales >/dev/null 2>&1 || true
    rm -f /etc/apt/apt.conf.d/99platformd-network
    rm -rf /var/lib/apt/lists/*
    ;;
esac`

type Engine interface {
	InspectImage(context.Context, string) (containerengine.Image, error)
	CreateContainer(context.Context, containerengine.ContainerSpec) (containerengine.Container, error)
	StartContainerAttached(context.Context, string, io.WriteCloser, io.WriteCloser) (<-chan error, error)
	WaitContainer(context.Context, string) (int32, error)
	RemoveContainer(context.Context, string, bool) error
	CommitDerivedImage(context.Context, containerengine.DerivedImageRequest) (containerengine.Image, error)
	ImagesByLabel(context.Context, string) ([]containerengine.Image, error)
	RemoveImage(context.Context, string) error
}

type GrowthGate interface {
	PermitGrowth(context.Context) error
}

type Config struct {
	Engine        Engine
	Growth        GrowthGate
	CacheRoot     string
	HTTPClient    *http.Client
	ResolveSource func(context.Context, Recipe) (string, error)
}

type BuildRequest struct {
	Base         containerengine.Image
	Extensions   []state.ManagedPostgresExtension
	ProjectID    string
	PostgresID   string
	Network      string
	DNSServers   []string
	CgroupParent string
	Progress     func(string)
}

type Builder struct {
	config Config
	mu     sync.Mutex
	locks  map[string]*sync.Mutex
}

func New(config Config) (*Builder, error) {
	if config.Engine == nil || config.Growth == nil || config.CacheRoot == "" {
		return nil, errors.New("PostgreSQL extension builder dependencies are incomplete")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	if config.ResolveSource == nil {
		cache := sourceCache{root: config.CacheRoot, client: config.HTTPClient}
		config.ResolveSource = cache.ensure
	}
	return &Builder{config: config, locks: make(map[string]*sync.Mutex)}, nil
}

func (builder *Builder) Ensure(ctx context.Context, request BuildRequest) (containerengine.Image, error) {
	if request.Base.ID == "" || request.Base.Digest == "" || request.Base.Architecture == "" || request.Base.OS != "linux" || len(request.Extensions) == 0 || request.ProjectID == "" || request.PostgresID == "" || request.Network == "" || len(request.DNSServers) == 0 {
		return containerengine.Image{}, errors.New("PostgreSQL extension build request is incomplete")
	}
	cacheKey, err := CacheKey(request.Base.Digest, request.Base.Architecture, request.Extensions)
	if err != nil {
		return containerengine.Image{}, err
	}
	lock := builder.cacheLock(cacheKey)
	lock.Lock()
	defer lock.Unlock()
	reference := "localhost/platformd/postgres-derived:" + cacheKey
	if image, err := builder.config.Engine.InspectImage(ctx, reference); err == nil {
		if err := validateCachedImage(image, request.Base, request.Extensions, cacheKey); err != nil {
			return containerengine.Image{}, err
		}
		progress(request.Progress, "using_cached_image")
		return image, nil
	}
	if err := builder.config.Growth.PermitGrowth(ctx); err != nil {
		return containerengine.Image{}, fmt.Errorf("PostgreSQL extension image is not cached: %w", err)
	}
	progress(request.Progress, "downloading_source")
	recipe, err := ValidateDesired(request.Extensions[0])
	if err != nil {
		return containerengine.Image{}, err
	}
	source, err := builder.config.ResolveSource(ctx, recipe)
	if err != nil {
		return containerengine.Image{}, err
	}
	container, err := builder.config.Engine.CreateContainer(ctx, containerengine.ContainerSpec{
		ImageID:    request.Base.ID,
		Name:       "platformd-postgres-extension-" + cacheKey[:20],
		Entrypoint: []string{"/bin/sh", "-c"},
		Command:    []string{buildScript},
		Labels: map[string]string{
			OwnerLabel:                 "postgres-extension-builder",
			"io.platformd.project-id":  request.ProjectID,
			"io.platformd.postgres-id": request.PostgresID,
		},
		Network:      request.Network,
		DNSServers:   append([]string(nil), request.DNSServers...),
		Mounts:       []containerengine.Mount{{Source: source, Destination: "/platformd/vector.tar.gz", ReadOnly: true}},
		LogDriver:    containerengine.ContainerLogNone,
		CgroupParent: request.CgroupParent,
	})
	if err != nil {
		return containerengine.Image{}, fmt.Errorf("create PostgreSQL extension builder: %w", err)
	}
	defer func() { _ = builder.config.Engine.RemoveContainer(context.Background(), container.ID, true) }()
	progress(request.Progress, "building_image")
	fields := []systemevent.Field{
		systemevent.String("project_id", request.ProjectID),
		systemevent.String("postgres_id", request.PostgresID),
		systemevent.String("cache_key", cacheKey),
	}
	systemevent.Info("postgres_extension_build_started", fields...)
	stdoutFields := append(append([]systemevent.Field(nil), fields...), systemevent.String("stream", "stdout"))
	stderrFields := append(append([]systemevent.Field(nil), fields...), systemevent.String("stream", "stderr"))
	stdout := systemevent.NewLineWriter("postgres_extension_build_output", stdoutFields...)
	stderr := systemevent.NewLineWriter("postgres_extension_build_output", stderrFields...)
	attached, err := builder.config.Engine.StartContainerAttached(ctx, container.ID, stdout, stderr)
	if err != nil {
		return containerengine.Image{}, errors.Join(
			fmt.Errorf("start PostgreSQL extension builder: %w", err),
			stdout.Close(),
			stderr.Close(),
		)
	}
	exitCode, err := builder.config.Engine.WaitContainer(ctx, container.ID)
	if err != nil {
		return containerengine.Image{}, err
	}
	if attachErr, ok := <-attached; ok && attachErr != nil {
		return containerengine.Image{}, fmt.Errorf("read PostgreSQL extension build output: %w", attachErr)
	}
	if exitCode != 0 {
		return containerengine.Image{}, fmt.Errorf("PostgreSQL extension build exited with code %d; inspect event=postgres_extension_build_output in the platform journal", exitCode)
	}
	systemevent.Info("postgres_extension_build_completed", fields...)
	recipeSet, err := RecipeSet(request.Extensions)
	if err != nil {
		return containerengine.Image{}, err
	}
	progress(request.Progress, "committing_image")
	image, err := builder.config.Engine.CommitDerivedImage(ctx, containerengine.DerivedImageRequest{
		ContainerID: container.ID, BaseImageID: request.Base.ID, Reference: reference,
		Labels: map[string]string{
			OwnerLabel: DerivedOwner, CacheKeyLabel: cacheKey,
			BaseDigestLabel: request.Base.Digest, RecipeSetLabel: recipeSet,
		},
	})
	if err != nil {
		return containerengine.Image{}, err
	}
	if err := validateCachedImage(image, request.Base, request.Extensions, cacheKey); err != nil {
		return containerengine.Image{}, err
	}
	return image, nil
}

func (builder *Builder) GarbageCollect(ctx context.Context, required map[string]struct{}) error {
	images, err := builder.config.Engine.ImagesByLabel(ctx, OwnerLabel+"="+DerivedOwner)
	if err != nil {
		return err
	}
	var failures []error
	for _, image := range images {
		cacheKey := image.Labels[CacheKeyLabel]
		if cacheKey == "" {
			continue
		}
		if _, keep := required[cacheKey]; keep {
			continue
		}
		if err := builder.config.Engine.RemoveImage(ctx, image.ID); err != nil {
			// Removal is deliberately non-forced. An image still referenced by a
			// runtime container is left for the next reconciliation pass.
			failures = append(failures, fmt.Errorf("remove unused PostgreSQL extension image %s: %w", image.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (builder *Builder) cacheLock(key string) *sync.Mutex {
	builder.mu.Lock()
	defer builder.mu.Unlock()
	lock := builder.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		builder.locks[key] = lock
	}
	return lock
}

func validateCachedImage(image, base containerengine.Image, extensions []state.ManagedPostgresExtension, cacheKey string) error {
	recipeSet, err := RecipeSet(extensions)
	if err != nil {
		return err
	}
	if image.ID == "" || image.Architecture != base.Architecture || image.OS != base.OS ||
		image.Labels[OwnerLabel] != DerivedOwner || image.Labels[CacheKeyLabel] != cacheKey ||
		image.Labels[BaseDigestLabel] != base.Digest || image.Labels[RecipeSetLabel] != recipeSet {
		return errors.New("cached PostgreSQL extension image metadata does not match the requested recipe")
	}
	return nil
}

func progress(callback func(string), value string) {
	if callback != nil {
		callback(value)
	}
}
