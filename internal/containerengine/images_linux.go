//go:build linux && amd64 && cgo

package containerengine

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/containers/buildah"
	buildahDefine "github.com/containers/buildah/define"
	"github.com/containers/podman/v5/libpod"
	"github.com/opencontainers/runtime-spec/specs-go"
	"go.podman.io/common/libimage"
	commonconfig "go.podman.io/common/pkg/config"
	"go.podman.io/image/v5/manifest"
	"go.podman.io/image/v5/pkg/strslice"
	imagetypes "go.podman.io/image/v5/types"
)

func (e *Engine) Build(ctx context.Context, request BuildRequest) (Image, error) {
	if request.ContextDirectory == "" || request.Dockerfile == "" || request.Reference == "" ||
		request.Network == "" || request.Timeout <= 0 || request.Log == nil {
		return Image{}, errors.New("image build request is incomplete")
	}
	buildContext, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	log := io.MultiWriter(request.Log)
	id, _, err := e.runtime.Build(buildContext, buildahDefine.BuildOptions{
		ContextDirectory:    request.ContextDirectory,
		PullPolicy:          buildahDefine.PullIfMissing,
		SignaturePolicyPath: e.config.SignaturePolicy,
		SystemContext:       &imagetypes.SystemContext{SystemRegistriesConfPath: e.config.RegistriesConf},
		NamespaceOptions: []buildahDefine.NamespaceOption{{
			Name: string(specs.NetworkNamespace), Path: request.Network,
		}},
		ConfigureNetwork:        buildahDefine.NetworkEnabled,
		NetworkInterface:        e.runtime.Network(),
		Output:                  request.Reference,
		Out:                     log,
		Err:                     log,
		ReportWriter:            log,
		CommonBuildOpts:         &buildahDefine.CommonBuildOptions{},
		Layers:                  true,
		RemoveIntermediateCtrs:  true,
		ForceRmIntermediateCtrs: true,
	}, request.Dockerfile)
	if err != nil {
		if errors.Is(buildContext.Err(), context.DeadlineExceeded) {
			return Image{}, fmt.Errorf("build image %s exceeded %s: %w", request.Reference, request.Timeout, buildContext.Err())
		}
		return Image{}, fmt.Errorf("build image %s: %w", request.Reference, err)
	}
	return e.inspectImage(ctx, id)
}

func (e *Engine) Pull(ctx context.Context, request PullRequest) (Image, error) {
	if request.Reference == "" {
		return Image{}, fmt.Errorf("image reference is empty")
	}
	policy := commonconfig.PullPolicyMissing
	if request.Refresh {
		policy = commonconfig.PullPolicyAlways
	}

	images, err := e.runtime.LibimageRuntime().Pull(ctx, request.Reference, policy, &libimage.PullOptions{
		CopyOptions: libimage.CopyOptions{
			Username:            request.Username,
			Password:            request.Password,
			SignaturePolicyPath: e.config.SignaturePolicy,
		},
	})
	if err != nil {
		return Image{}, fmt.Errorf("pull image %s: %w", request.Reference, err)
	}
	if len(images) != 1 {
		return Image{}, fmt.Errorf("pull image %s returned %d images", request.Reference, len(images))
	}
	return e.inspectImage(ctx, images[0].ID())
}

func (e *Engine) InspectImage(ctx context.Context, idOrName string) (Image, error) {
	return e.inspectImage(ctx, idOrName)
}

func (e *Engine) inspectImage(ctx context.Context, idOrName string) (Image, error) {
	image, _, err := e.runtime.LibimageRuntime().LookupImage(idOrName, nil)
	if err != nil {
		return Image{}, fmt.Errorf("lookup image %s: %w", idOrName, err)
	}
	data, err := image.Inspect(ctx, &libimage.InspectOptions{WithSize: true})
	if err != nil {
		return Image{}, fmt.Errorf("inspect image %s: %w", idOrName, err)
	}
	result := Image{
		ID:           image.ID(),
		Digest:       image.Digest().String(),
		Names:        image.Names(),
		User:         data.User,
		Architecture: data.Architecture,
		OS:           data.Os,
		Labels:       cloneStrings(data.Labels),
		Size:         data.Size,
	}
	if data.Created != nil {
		result.Created = *data.Created
	}
	if data.Config != nil {
		result.Entrypoint = append([]string(nil), data.Config.Entrypoint...)
		result.Command = append([]string(nil), data.Config.Cmd...)
	}
	return result, nil
}

func (e *Engine) CommitDerivedImage(ctx context.Context, request DerivedImageRequest) (Image, error) {
	if request.ContainerID == "" || request.BaseImageID == "" || request.Reference == "" {
		return Image{}, errors.New("derived image request is incomplete")
	}
	base, _, err := e.runtime.LibimageRuntime().LookupImage(request.BaseImageID, nil)
	if err != nil {
		return Image{}, fmt.Errorf("lookup derived image base %s: %w", request.BaseImageID, err)
	}
	baseData, err := base.Inspect(ctx, nil)
	if err != nil {
		return Image{}, fmt.Errorf("inspect derived image base %s: %w", request.BaseImageID, err)
	}
	if baseData.Config == nil {
		return Image{}, errors.New("derived image base has no OCI configuration")
	}
	container, err := e.lookupContainer(request.ContainerID)
	if err != nil {
		return Image{}, err
	}
	labels := make(map[string]string, len(baseData.Config.Labels)+len(request.Labels))
	for key, value := range baseData.Config.Labels {
		labels[key] = value
	}
	for key, value := range request.Labels {
		if key == "" || value == "" {
			return Image{}, errors.New("derived image labels cannot be empty")
		}
		labels[key] = value
	}
	// Commit the builder rootfs, but keep the base runtime contract exposed by
	// the OCI image. In particular, the builder has a shell entrypoint and a
	// read-only source mount that must not become the derived image defaults.
	override := &manifest.Schema2Config{
		User:         baseData.Config.User,
		Env:          append([]string(nil), baseData.Config.Env...),
		Cmd:          strslice.StrSlice(append([]string(nil), baseData.Config.Cmd...)),
		WorkingDir:   baseData.Config.WorkingDir,
		Entrypoint:   strslice.StrSlice(append([]string(nil), baseData.Config.Entrypoint...)),
		Labels:       labels,
		StopSignal:   baseData.Config.StopSignal,
		Healthcheck:  baseData.HealthCheck,
		ArgsEscaped:  baseData.Config.ArgsEscaped,
		Volumes:      make(map[string]struct{}, len(baseData.Config.Volumes)),
		ExposedPorts: make(manifest.Schema2PortSet, len(baseData.Config.ExposedPorts)),
	}
	for volume := range baseData.Config.Volumes {
		override.Volumes[volume] = struct{}{}
	}
	for port := range baseData.Config.ExposedPorts {
		override.ExposedPorts[manifest.Schema2Port(port)] = struct{}{}
	}
	committed, err := container.Commit(ctx, request.Reference, libpod.ContainerCommitOptions{
		CommitOptions: buildah.CommitOptions{
			SignaturePolicyPath: e.config.SignaturePolicy,
			OverrideConfig:      override,
		},
		IncludeVolumes: false,
		Pause:          false,
	})
	if err != nil {
		return Image{}, fmt.Errorf("commit derived image %s: %w", request.Reference, err)
	}
	return e.inspectImage(ctx, committed.ID())
}

func (e *Engine) ImagesByLabel(ctx context.Context, label string) ([]Image, error) {
	if label == "" {
		return nil, errors.New("image label filter is empty")
	}
	images, err := e.runtime.LibimageRuntime().ListImages(ctx, &libimage.ListImagesOptions{
		Filters: []string{"label=" + label},
	})
	if err != nil {
		return nil, fmt.Errorf("list images by label %s: %w", label, err)
	}
	result := make([]Image, 0, len(images))
	for _, image := range images {
		inspected, err := e.inspectImage(ctx, image.ID())
		if err != nil {
			return nil, err
		}
		result = append(result, inspected)
	}
	return result, nil
}

func (e *Engine) RemoveImage(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("image ID is empty")
	}
	_, failures := e.runtime.LibimageRuntime().RemoveImages(ctx, []string{id}, &libimage.RemoveImagesOptions{
		Force:   false,
		Ignore:  true,
		NoPrune: true,
	})
	return errors.Join(failures...)
}
