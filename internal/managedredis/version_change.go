package managedredis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/state"
)

type VersionChangeInput struct {
	ResourceID  string
	ImageTag    string
	ImageDigest string
	Actor       Actor
	Progress    func(string)
}

// ChangeVersion always migrates through a new volume. Until the single SQLite
// pointer switch succeeds, the old runtime remains the recovery source.
func (controller *Controller) ChangeVersion(ctx context.Context, input VersionChangeInput) (resultErr error) {
	if ctx == nil || !safePathComponent(input.ResourceID) || input.ImageDigest == "" {
		return fmt.Errorf("%w: version change target is incomplete", ErrInvalidInput)
	}
	if _, err := managedimages.Reference(managedimages.Redis, input.ImageTag); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if !validVersionChangeActor(input.Actor) {
		return fmt.Errorf("%w: version change actor is invalid", ErrInvalidInput)
	}
	progress := input.Progress
	if progress == nil {
		progress = func(string) {}
	}
	lease, err := controller.admission.Begin("redis_version_change", input.ResourceID)
	if err != nil {
		return err
	}
	defer lease.Release()
	lock := controller.resourceLock(input.ResourceID)
	lock.Lock()
	defer lock.Unlock()

	resource, err := controller.store.ManagedRedis(ctx, input.ResourceID)
	if err != nil {
		return err
	}
	if resource.ImageDigest == input.ImageDigest {
		return fmt.Errorf("%w: target digest is already active", ErrInvalidInput)
	}
	oldRuntime, oldRunning := controller.activeRuntime(resource.ID)
	if !oldRunning {
		return ErrNotRunning
	}
	if oldRuntime.resource.VolumeID != resource.VolumeID || oldRuntime.resource.ImageDigest != resource.ImageDigest {
		return errors.New("managed Redis runtime does not match the active pointer")
	}
	target := resource
	target.ImageTag = input.ImageTag
	target.ImageDigest = input.ImageDigest
	progress("resolving_target_image")
	image, err := controller.resolveImage(ctx, target)
	if err != nil {
		return err
	}
	if err := controller.growth.PermitGrowth(ctx); err != nil {
		return fmt.Errorf("create managed Redis version-change volume: %w", err)
	}
	timestamp := controller.now()
	identifiers, err := controller.restoreIdentifiers(timestamp)
	if err != nil {
		return err
	}
	volumeID, runtimeID, auditID, correlationID := identifiers[0], identifiers[1], identifiers[2], identifiers[3]
	if volumeID == resource.VolumeID || runtimeID == resource.ID || volumeID == runtimeID {
		return errors.New("managed Redis version-change identifiers are not unique")
	}
	target.VolumeID = volumeID
	if err := controller.beginCandidateDeployment(ctx, runtimeID, target, timestamp.UnixMilli()); err != nil {
		return err
	}
	defer controller.finishCandidateDeployment(ctx, runtimeID, &resultErr)
	password, err := controller.password(resource)
	if err != nil {
		return fmt.Errorf("open managed Redis password: %w", err)
	}
	placement, err := controller.placement(resource)
	if err != nil {
		return fmt.Errorf("place managed Redis version-change candidate: %w", err)
	}
	candidate := containerengine.Container{}
	candidateStarted := false
	oldWithdrawn := false
	oldStopped := false
	committed := false
	var releaseMaintenance func() error
	defer func() {
		if !committed {
			cleanupErr := controller.removeRestoreCandidate(
				candidate.ID, candidateStarted, resource.ProjectID, volumeID, runtimeID,
			)
			var recoveryErr error
			if oldWithdrawn {
				recoveryErr = controller.recoverVersionChangeSource(oldRuntime, oldStopped, password)
			}
			resultErr = errors.Join(resultErr, cleanupErr, recoveryErr)
		}
		if releaseMaintenance != nil {
			resultErr = errors.Join(resultErr, releaseMaintenance())
		}
	}()

	endpoint, err := redisVersionEndpoint(controller.engine, oldRuntime)
	if err != nil {
		return err
	}
	if !controller.beginMaintenance(resource.ID) {
		return ErrMaintenance
	}
	defer controller.endMaintenance(resource.ID)
	releaseMaintenance, err = controller.maintenance.BlockDatabase(ctx, resource.ProjectID, endpoint, Port)
	if err != nil {
		return fmt.Errorf("block new managed Redis connections: %w", err)
	}
	progress("withdrawing_endpoint")
	if err := controller.publisher.WithdrawRedis(resource); err != nil {
		return err
	}
	oldWithdrawn = true
	progress("draining_clients")
	if err := controller.drainAndKillClients(ctx, oldRuntime, password); err != nil {
		return fmt.Errorf("drain managed Redis clients: %w", err)
	}
	volume, err := createRestoreVolume(controller.volumeRoot, resource.ProjectID, volumeID)
	if err != nil {
		return err
	}
	progress("saving_source")
	source, err := controller.openFreshRDB(ctx, oldRuntime, password)
	if err != nil {
		return fmt.Errorf("save managed Redis before version change: %w", err)
	}
	sourceClosed := false
	defer func() {
		if !sourceClosed {
			resultErr = errors.Join(resultErr, source.Close())
		}
	}()
	if err := controller.engine.StopContainer(oldRuntime.container.ID, stopTimeoutSeconds); err != nil {
		return fmt.Errorf("stop managed Redis before version change: %w", err)
	}
	oldStopped = true
	controller.clearActive(resource.ID)
	progress("copying_rdb")
	if err := copyVersionChangeRDB(ctx, volume, source); err != nil {
		return err
	}
	if err := source.Close(); err != nil {
		return fmt.Errorf("close managed Redis version-change RDB: %w", err)
	}
	sourceClosed = true
	configPath, err := writeConfig(controller.generatedRoot, runtimeID, password)
	if err != nil {
		return err
	}
	candidate, err = controller.createContainerAttempt(ctx, target, runtimeID, image.ID, placement, volume, configPath)
	if err != nil {
		return err
	}
	if err := controller.engine.StartContainer(ctx, candidate.ID); err != nil {
		return fmt.Errorf("start managed Redis version-change candidate: %w", err)
	}
	candidateStarted = true
	progress("validating_target")
	candidate, err = controller.waitReady(ctx, candidate.ID, placement.NetworkName, password)
	if err != nil {
		return fmt.Errorf("validate managed Redis version-change candidate: %w", err)
	}
	progress("switching_active_pointer")
	err = controller.store.SwitchManagedRedisVolume(ctx, state.SwitchManagedRedisVolume{
		ResourceID: resource.ID, ExpectedVolumeID: resource.VolumeID, VolumeID: volumeID,
		ExpectedImageTag: resource.ImageTag, ExpectedImageDigest: resource.ImageDigest,
		ImageTag: target.ImageTag, ImageDigest: target.ImageDigest,
		Action: "redis.version_change", AuditEventID: auditID,
		ActorKind: input.Actor.Kind, ActorID: input.Actor.ID, ActorEmail: input.Actor.Email,
		RequestCorrelationID: correlationID, UpdatedAtMillis: timestamp.UnixMilli(),
	})
	if err != nil {
		return err
	}
	committed = true
	target.UpdatedAtMillis = timestamp.UnixMilli()
	controller.setActive(resource.ID, activeRuntime{
		resource: target, container: candidate, network: placement.NetworkName, runtimeID: runtimeID,
	})
	if err := controller.activateCandidateDeployment(ctx, resource.ID, runtimeID); err != nil {
		return err
	}
	publishErr := controller.publisher.PublishRedis(target, candidate)
	cleanupErr := controller.removeReplacedRuntime(ctx, oldRuntime, true, resource)
	progress("complete")
	return errors.Join(publishErr, cleanupErr)
}

func (controller *Controller) drainAndKillClients(
	ctx context.Context,
	runtime activeRuntime,
	password string,
) error {
	timer := time.NewTimer(controller.maintenanceDrain)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	container, err := controller.engine.InspectContainer(runtime.container.ID)
	if err != nil {
		return err
	}
	addresses := container.IPs[runtime.network]
	if container.State != "running" || len(addresses) != 1 {
		return ErrNotRunning
	}
	connection, err := controller.dial(
		ctx, net.JoinHostPort(addresses[0], fmt.Sprint(Port)), password,
	)
	if err != nil {
		return err
	}
	return errors.Join(connection.KillNormalClients(ctx), connection.Close())
}

func redisVersionEndpoint(engine Engine, runtime activeRuntime) (netip.Addr, error) {
	container, err := engine.InspectContainer(runtime.container.ID)
	if err != nil {
		return netip.Addr{}, err
	}
	addresses := container.IPs[runtime.network]
	if container.State != "running" || len(addresses) != 1 {
		return netip.Addr{}, ErrNotRunning
	}
	address, err := netip.ParseAddr(addresses[0])
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse managed Redis project address %q: %w", addresses[0], err)
	}
	if !address.Is4() {
		return netip.Addr{}, errors.New("managed Redis project address is not IPv4")
	}
	return address, nil
}

func validVersionChangeActor(actor Actor) bool {
	if actor.ID == "" {
		return false
	}
	switch actor.Kind {
	case "access":
		return actor.Email != ""
	case "token":
		return actor.Email == ""
	default:
		return false
	}
}

func copyVersionChangeRDB(ctx context.Context, targetVolume string, source io.Reader) error {
	if err := writeRestoreRDB(ctx, targetVolume, source); err != nil {
		return fmt.Errorf("copy managed Redis version-change RDB: %w", err)
	}
	return nil
}

func (controller *Controller) recoverVersionChangeSource(runtime activeRuntime, stopped bool, password string) error {
	if stopped {
		ctx, cancel := context.WithTimeout(context.Background(), controller.readyTimeout+time.Duration(stopTimeoutSeconds)*time.Second)
		defer cancel()
		if err := controller.engine.StartContainer(ctx, runtime.container.ID); err != nil {
			return fmt.Errorf("restart managed Redis after failed version change: %w", err)
		}
		ready, err := controller.waitReady(ctx, runtime.container.ID, runtime.network, password)
		if err != nil {
			return fmt.Errorf("validate managed Redis after failed version change: %w", err)
		}
		runtime.container = ready
	}
	controller.setActive(runtime.resource.ID, runtime)
	if err := controller.publisher.PublishRedis(runtime.resource, runtime.container); err != nil {
		return fmt.Errorf("republish managed Redis after failed version change: %w", err)
	}
	return nil
}
