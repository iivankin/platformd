package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// ChangeVersion streams a logical dump into a fresh target volume. The old
// pointer remains authoritative until the single SQLite switch succeeds.
func (controller *Controller) ChangeVersion(ctx context.Context, input VersionChangeInput) (resultErr error) {
	if ctx == nil || !safePathComponent(input.ResourceID) || input.ImageDigest == "" {
		return fmt.Errorf("%w: version change target is incomplete", ErrInvalidInput)
	}
	if _, err := managedimages.Reference(managedimages.PostgreSQL, input.ImageTag); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if !validVersionChangeActor(input.Actor) {
		return fmt.Errorf("%w: version change actor is invalid", ErrInvalidInput)
	}
	progress := input.Progress
	if progress == nil {
		progress = func(string) {}
	}
	lease, err := controller.admission.Begin("postgres_version_change", input.ResourceID)
	if err != nil {
		return err
	}
	defer lease.Release()
	lock := controller.resourceLock(input.ResourceID)
	lock.Lock()
	defer lock.Unlock()

	resource, err := controller.store.ManagedPostgres(ctx, input.ResourceID)
	if err != nil {
		return err
	}
	if resource.ImageDigest == input.ImageDigest {
		return fmt.Errorf("%w: target digest is already active", ErrInvalidInput)
	}
	oldRuntime, running := controller.activeRuntime(resource.ID)
	if !running {
		return ErrNotRunning
	}
	if oldRuntime.resource.VolumeID != resource.VolumeID || oldRuntime.resource.ImageDigest != resource.ImageDigest {
		return errors.New("managed PostgreSQL runtime does not match the active pointer")
	}
	restoreExtensions, err := controller.restoreExtensionNames(ctx, resource.ID, oldRuntime, true)
	if err != nil {
		return err
	}
	endpoint, err := postgresVersionEndpoint(controller.engine, oldRuntime)
	if err != nil {
		return err
	}
	ownerPassword, err := controller.ownerPassword(resource)
	if err != nil {
		return fmt.Errorf("open managed PostgreSQL owner password: %w", err)
	}
	bootstrapPassword, err := controller.bootstrapPassword(resource)
	if err != nil {
		return fmt.Errorf("open managed PostgreSQL bootstrap password: %w", err)
	}
	placement, err := controller.placement(resource)
	if err != nil {
		return fmt.Errorf("place managed PostgreSQL version-change candidate: %w", err)
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
		return fmt.Errorf("create managed PostgreSQL version-change volume: %w", err)
	}

	timestamp := controller.now()
	identifiers, err := controller.restoreIdentifiers(timestamp)
	if err != nil {
		return err
	}
	volumeID, runtimeID, auditID, correlationID := identifiers[0], identifiers[1], identifiers[2], identifiers[3]
	if volumeID == resource.VolumeID || runtimeID == resource.ID || volumeID == runtimeID {
		return errors.New("managed PostgreSQL version-change identifiers are not unique")
	}
	target.VolumeID = volumeID
	if err := controller.beginCandidateDeployment(ctx, runtimeID, target, timestamp.UnixMilli()); err != nil {
		return err
	}
	defer controller.finishCandidateDeployment(ctx, runtimeID, &resultErr)
	candidate := containerengine.Container{}
	candidateStarted := false
	oldWithdrawn := false
	oldStopped := false
	committed := false
	var releaseMaintenance func() error
	defer func() {
		if !committed {
			cleanupErr := controller.removePostgresRestoreCandidate(
				candidate.ID, candidateStarted, resource.ProjectID, volumeID,
			)
			var recoveryErr error
			if oldWithdrawn {
				recoveryErr = controller.recoverVersionChangeSource(
					oldRuntime, oldStopped, ownerPassword, bootstrapPassword,
				)
			}
			resultErr = errors.Join(resultErr, cleanupErr, recoveryErr)
		}
		if releaseMaintenance != nil {
			resultErr = errors.Join(resultErr, releaseMaintenance())
		}
	}()

	if !controller.beginMaintenance(resource.ID) {
		return ErrMaintenance
	}
	defer controller.endMaintenance(resource.ID)
	releaseMaintenance, err = controller.maintenance.BlockDatabase(ctx, resource.ProjectID, endpoint, Port)
	if err != nil {
		return fmt.Errorf("block new managed PostgreSQL connections: %w", err)
	}
	progress("withdrawing_endpoint")
	if err := controller.publisher.WithdrawPostgres(resource); err != nil {
		return err
	}
	oldWithdrawn = true
	progress("draining_clients")
	if err := controller.drainAndTerminateClients(ctx, oldRuntime, ownerPassword); err != nil {
		return err
	}

	progress("creating_target")
	volume, err := createPostgresRestoreVolume(controller.volumeRoot, resource.ProjectID, volumeID)
	if err != nil {
		return err
	}
	candidate, err = controller.createContainerAttempt(
		ctx, target, runtimeID, image.ID, placement, volume, bootstrapPassword,
	)
	if err != nil {
		return err
	}
	if err := controller.engine.StartContainer(ctx, candidate.ID); err != nil {
		return fmt.Errorf("start managed PostgreSQL version-change candidate: %w", err)
	}
	candidateStarted = true
	candidate, err = controller.waitReady(
		ctx, candidate.ID, placement.NetworkName, target, ownerPassword, bootstrapPassword,
	)
	if err != nil {
		return fmt.Errorf("initialize managed PostgreSQL version-change candidate: %w", err)
	}
	if err := controller.prepareRestoreExtensions(
		ctx, target, candidate, placement.NetworkName, restoreExtensions,
	); err != nil {
		return err
	}
	progress("transferring_dump")
	if err := controller.transferVersionDump(ctx, oldRuntime, candidate.ID, target, ownerPassword); err != nil {
		return err
	}
	progress("validating_target")
	if err := controller.probeRestoredCandidate(ctx, candidate.ID, placement.NetworkName, target, ownerPassword); err != nil {
		return fmt.Errorf("validate managed PostgreSQL version-change candidate: %w", err)
	}
	if err := controller.engine.StopContainer(oldRuntime.container.ID, stopTimeoutSeconds); err != nil {
		return fmt.Errorf("stop managed PostgreSQL source after version transfer: %w", err)
	}
	oldStopped = true
	controller.clearActive(resource.ID)

	progress("switching_active_pointer")
	err = controller.store.SwitchManagedPostgresVolume(ctx, state.SwitchManagedPostgresVolume{
		ResourceID: resource.ID, ExpectedVolumeID: resource.VolumeID, VolumeID: volumeID,
		ExpectedImageTag: resource.ImageTag, ExpectedImageDigest: resource.ImageDigest,
		ImageTag: target.ImageTag, ImageDigest: target.ImageDigest,
		Action: "postgres.version_change", AuditEventID: auditID,
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
	publishErr := controller.publisher.PublishPostgres(target, candidate)
	cleanupErr := controller.removeReplacedPostgres(ctx, oldRuntime, true, resource)
	progress("complete")
	return errors.Join(publishErr, cleanupErr)
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

func postgresVersionEndpoint(engine Engine, runtime activeRuntime) (netip.Addr, error) {
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
		return netip.Addr{}, fmt.Errorf("parse managed PostgreSQL project address %q: %w", addresses[0], err)
	}
	if !address.Is4() {
		return netip.Addr{}, errors.New("managed PostgreSQL project address is not IPv4")
	}
	return address, nil
}

func (controller *Controller) drainAndTerminateClients(
	ctx context.Context,
	runtime activeRuntime,
	ownerPassword string,
) error {
	timer := time.NewTimer(controller.maintenanceDrain)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	address, err := controller.runtimeAddress(runtime)
	if err != nil {
		return err
	}
	connection, err := controller.dial(
		ctx, address, runtime.resource.OwnerUsername, ownerPassword, runtime.resource.DatabaseName,
	)
	if err != nil {
		return err
	}
	_, queryErr := connection.Query(ctx, `SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname = current_database() AND usename = current_user AND pid <> pg_backend_pid()`)
	return errors.Join(queryErr, connection.Close(ctx))
}

func (controller *Controller) transferVersionDump(
	ctx context.Context,
	source activeRuntime,
	targetContainerID string,
	target state.ManagedPostgres,
	ownerPassword string,
) error {
	transferContext, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	dumpDone := make(chan error, 1)
	go func() {
		dumpErr := controller.dumpVersionSource(transferContext, source, ownerPassword, writer)
		_ = writer.CloseWithError(dumpErr)
		dumpDone <- dumpErr
	}()
	restoreErr := controller.restoreDump(transferContext, targetContainerID, target, ownerPassword, reader)
	if restoreErr != nil {
		cancel()
		_ = reader.CloseWithError(restoreErr)
	} else {
		_ = reader.Close()
	}
	dumpErr := <-dumpDone
	return errors.Join(restoreErr, dumpErr)
}

func (controller *Controller) dumpVersionSource(
	ctx context.Context,
	source activeRuntime,
	ownerPassword string,
	output io.Writer,
) error {
	var stderr boundedDiagnostic
	code, execErr := controller.engine.ExecContainer(ctx, source.container.ID, containerengine.ExecRequest{
		Command: []string{
			"pg_dump", "--format=custom", "--no-owner", "--no-acl",
			"--host=127.0.0.1", "--port=5432", "--dbname=" + source.resource.DatabaseName,
			"--username=" + source.resource.OwnerUsername,
		},
		Environment: map[string]string{"PGPASSWORD": ownerPassword},
		Stdout:      output, Stderr: &stderr,
	})
	if execErr != nil {
		return fmt.Errorf("pg_dump exited with code %d: %s: %w", code, stderr.String(), execErr)
	}
	if code != 0 {
		return fmt.Errorf("pg_dump exited with code %d: %s", code, stderr.String())
	}
	return nil
}

func (controller *Controller) recoverVersionChangeSource(
	runtime activeRuntime,
	stopped bool,
	ownerPassword string,
	bootstrapPassword string,
) error {
	if stopped {
		ctx, cancel := context.WithTimeout(context.Background(), controller.readyTimeout+30*time.Second)
		defer cancel()
		if err := controller.engine.StartContainer(ctx, runtime.container.ID); err != nil {
			return fmt.Errorf("restart managed PostgreSQL after failed version change: %w", err)
		}
		ready, err := controller.waitReady(
			ctx, runtime.container.ID, runtime.network, runtime.resource, ownerPassword, bootstrapPassword,
		)
		if err != nil {
			return fmt.Errorf("validate managed PostgreSQL after failed version change: %w", err)
		}
		runtime.container = ready
	}
	controller.setActive(runtime.resource.ID, runtime)
	if err := controller.publisher.PublishPostgres(runtime.resource, runtime.container); err != nil {
		return fmt.Errorf("republish managed PostgreSQL after failed version change: %w", err)
	}
	return nil
}
