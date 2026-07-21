package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
)

// RestoreReplace imports into an unpublished PostgreSQL container and switches
// the single durable volume pointer only after pg_restore and an owner-authenticated
// readiness probe succeed. Candidate identity remains process-local.
func (controller *Controller) RestoreReplace(
	ctx context.Context,
	resourceID string,
	dump io.Reader,
	actor Actor,
) (resultErr error) {
	if ctx == nil || !safePathComponent(resourceID) || dump == nil {
		return errors.New("managed PostgreSQL restore input is invalid")
	}
	if !validRestoreActor(actor) {
		return errors.New("managed PostgreSQL restore actor is invalid")
	}
	lock := controller.resourceLock(resourceID)
	lock.Lock()
	defer lock.Unlock()

	resource, err := controller.store.ManagedPostgres(ctx, resourceID)
	if err != nil {
		return err
	}
	oldRuntime, oldRunning := controller.activeRuntime(resourceID)
	if oldRunning && oldRuntime.resource.VolumeID != resource.VolumeID {
		return errors.New("managed PostgreSQL runtime does not match the active volume")
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
		return fmt.Errorf("place managed PostgreSQL restore candidate: %w", err)
	}
	image, err := controller.resolveImage(ctx, resource)
	if err != nil {
		return err
	}
	if err := controller.growth.PermitGrowth(ctx); err != nil {
		return fmt.Errorf("create managed PostgreSQL restore volume: %w", err)
	}

	timestamp := controller.now()
	identifiers, err := controller.restoreIdentifiers(timestamp)
	if err != nil {
		return err
	}
	volumeID, runtimeID, auditID, correlationID := identifiers[0], identifiers[1], identifiers[2], identifiers[3]
	if volumeID == resource.VolumeID || runtimeID == resource.ID || volumeID == runtimeID {
		return errors.New("managed PostgreSQL restore identifiers are not unique")
	}
	if err := controller.beginCandidateDeployment(ctx, runtimeID, resource, timestamp.UnixMilli()); err != nil {
		return err
	}
	defer controller.finishCandidateDeployment(ctx, runtimeID, &resultErr)
	volume, err := createPostgresRestoreVolume(controller.volumeRoot, resource.ProjectID, volumeID)
	if err != nil {
		return err
	}
	candidate := containerengine.Container{}
	candidateStarted := false
	committed := false
	defer func() {
		if committed {
			return
		}
		resultErr = errors.Join(resultErr, controller.removePostgresRestoreCandidate(
			candidate.ID, candidateStarted, resource.ProjectID, volumeID,
		))
	}()
	candidate, err = controller.createContainerAttempt(
		ctx, resource, runtimeID, image.ID, placement, volume, bootstrapPassword,
	)
	if err != nil {
		return err
	}
	if err := controller.engine.StartContainer(ctx, candidate.ID); err != nil {
		return fmt.Errorf("start managed PostgreSQL restore candidate: %w", err)
	}
	candidateStarted = true
	candidate, err = controller.waitReady(
		ctx, candidate.ID, placement.NetworkName, resource, ownerPassword, bootstrapPassword,
	)
	if err != nil {
		return fmt.Errorf("initialize managed PostgreSQL restore candidate: %w", err)
	}
	if err := controller.restoreDump(
		ctx, candidate, placement.NetworkName, resource, ownerPassword, dump,
	); err != nil {
		return err
	}
	if err := controller.probeRestoredCandidate(ctx, candidate.ID, placement.NetworkName, resource, ownerPassword); err != nil {
		return fmt.Errorf("validate managed PostgreSQL restore candidate: %w", err)
	}

	oldStopped := false
	if oldRunning {
		if err := controller.publisher.WithdrawPostgres(oldRuntime.resource); err != nil {
			return fmt.Errorf("withdraw managed PostgreSQL before restore switch: %w", err)
		}
		if err := controller.engine.StopContainer(oldRuntime.container.ID, stopTimeoutSeconds); err != nil {
			return errors.Join(
				fmt.Errorf("stop managed PostgreSQL before restore switch: %w", err),
				controller.publisher.PublishPostgres(oldRuntime.resource, oldRuntime.container),
			)
		}
		oldStopped = true
	}
	err = controller.store.SwitchManagedPostgresVolume(ctx, state.SwitchManagedPostgresVolume{
		ResourceID: resource.ID, ExpectedVolumeID: resource.VolumeID, VolumeID: volumeID,
		Action: "postgres.restore", AuditEventID: auditID,
		ActorKind: actor.Kind, ActorID: actor.ID, ActorEmail: actor.Email,
		RequestCorrelationID: correlationID, UpdatedAtMillis: timestamp.UnixMilli(),
	})
	if err != nil {
		return errors.Join(err, controller.recoverOldPostgres(oldRuntime, oldRunning && oldStopped, ownerPassword, bootstrapPassword))
	}
	committed = true
	switched := resource
	switched.VolumeID = volumeID
	switched.UpdatedAtMillis = timestamp.UnixMilli()
	controller.setActive(resourceID, activeRuntime{
		resource: switched, container: candidate, network: placement.NetworkName, runtimeID: runtimeID,
	})
	if err := controller.activateCandidateDeployment(ctx, resourceID, runtimeID); err != nil {
		return err
	}
	publishErr := controller.publisher.PublishPostgres(switched, candidate)
	cleanupErr := controller.removeReplacedPostgres(ctx, oldRuntime, oldRunning, resource)
	return errors.Join(publishErr, cleanupErr)
}

func validRestoreActor(actor Actor) bool {
	if actor.ID == "" {
		return false
	}
	switch actor.Kind {
	case "access":
		return actor.Email != ""
	case "token", "system":
		return actor.Email == ""
	default:
		return false
	}
}

func (controller *Controller) restoreDump(
	ctx context.Context,
	candidate containerengine.Container,
	networkName string,
	resource state.ManagedPostgres,
	ownerPassword string,
	dump io.Reader,
) (resultErr error) {
	reader, restoreList, extensions, cleanup, err := controller.prepareRestoreArchive(ctx, candidate.ID, dump)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := controller.prepareRestoreExtensions(ctx, resource, candidate, networkName, extensions); err != nil {
		return err
	}
	if err := controller.writeRestoreList(ctx, candidate.ID, restoreList); err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, controller.removeRestoreList(ctx, candidate.ID))
	}()
	var stderr boundedDiagnostic
	code, execErr := controller.engine.ExecContainer(ctx, candidate.ID, containerengine.ExecRequest{
		Command: []string{
			"pg_restore", "--exit-on-error", "--no-owner", "--no-acl", "--use-list=" + postgresRestoreListPath,
			"--host=127.0.0.1", "--port=5432", "--dbname=" + resource.DatabaseName,
			"--username=" + resource.OwnerUsername,
		},
		Environment: map[string]string{"PGPASSWORD": ownerPassword},
		Stdin:       reader, Stderr: &stderr,
	})
	if execErr != nil || code != 0 {
		if execErr != nil {
			return fmt.Errorf("pg_restore exited with code %d: %s: %w", code, stderr.String(), execErr)
		}
		return fmt.Errorf("pg_restore exited with code %d: %s", code, stderr.String())
	}
	remaining, err := io.Copy(io.Discard, reader)
	if err != nil {
		return fmt.Errorf("finish managed PostgreSQL restore stream: %w", err)
	}
	if remaining != 0 {
		return errors.New("pg_restore did not consume the complete backup stream")
	}
	return nil
}

func (controller *Controller) probeRestoredCandidate(
	ctx context.Context,
	containerID string,
	networkName string,
	resource state.ManagedPostgres,
	ownerPassword string,
) error {
	container, err := controller.engine.InspectContainer(containerID)
	if err != nil {
		return err
	}
	addresses := container.IPs[networkName]
	if container.State != "running" || len(addresses) != 1 {
		return ErrNotRunning
	}
	connection, err := controller.dial(
		ctx, net.JoinHostPort(addresses[0], fmt.Sprint(Port)), resource.OwnerUsername, ownerPassword, resource.DatabaseName,
	)
	if err != nil {
		return err
	}
	return errors.Join(connection.Ping(ctx), connection.Close(ctx))
}

func (controller *Controller) restoreIdentifiers(timestamp time.Time) ([4]string, error) {
	var identifiers [4]string
	seen := make(map[string]struct{}, len(identifiers))
	for index := range identifiers {
		identifier, err := controller.newID(timestamp)
		if err != nil {
			return [4]string{}, fmt.Errorf("allocate managed PostgreSQL restore ID: %w", err)
		}
		if !safePathComponent(identifier) {
			return [4]string{}, errors.New("managed PostgreSQL restore ID source returned an invalid ID")
		}
		if _, duplicate := seen[identifier]; duplicate {
			return [4]string{}, errors.New("managed PostgreSQL restore ID source returned duplicate IDs")
		}
		seen[identifier] = struct{}{}
		identifiers[index] = identifier
	}
	return identifiers, nil
}

func (controller *Controller) recoverOldPostgres(
	runtime activeRuntime,
	stopped bool,
	ownerPassword string,
	bootstrapPassword string,
) error {
	if !stopped {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), controller.readyTimeout+30*time.Second)
	defer cancel()
	if err := controller.engine.StartContainer(ctx, runtime.container.ID); err != nil {
		return fmt.Errorf("restart managed PostgreSQL after failed restore switch: %w", err)
	}
	ready, err := controller.waitReady(
		ctx, runtime.container.ID, runtime.network, runtime.resource, ownerPassword, bootstrapPassword,
	)
	if err != nil {
		return fmt.Errorf("validate managed PostgreSQL after failed restore switch: %w", err)
	}
	runtime.container = ready
	controller.setActive(runtime.resource.ID, runtime)
	if err := controller.publisher.PublishPostgres(runtime.resource, ready); err != nil {
		return fmt.Errorf("republish managed PostgreSQL after failed restore switch: %w", err)
	}
	return nil
}

func (controller *Controller) removePostgresRestoreCandidate(containerID string, started bool, projectID, volumeID string) error {
	var stopErr error
	if started && containerID != "" {
		stopErr = controller.engine.StopContainer(containerID, stopTimeoutSeconds)
	}
	var removeErr error
	if containerID != "" {
		removeErr = controller.engine.RemoveContainer(context.Background(), containerID, true)
	}
	var volumeErr error
	if removeErr == nil {
		volumeErr = controller.removeManagedPostgresVolume(context.Background(), projectID, volumeID)
	}
	return errors.Join(stopErr, removeErr, volumeErr)
}

func (controller *Controller) removeReplacedPostgres(
	ctx context.Context,
	runtime activeRuntime,
	running bool,
	resource state.ManagedPostgres,
) error {
	if running {
		if err := controller.engine.RemoveContainer(ctx, runtime.container.ID, true); err != nil {
			return fmt.Errorf("remove replaced managed PostgreSQL container: %w", err)
		}
	}
	return controller.removeManagedPostgresVolume(ctx, resource.ProjectID, resource.VolumeID)
}

func createPostgresRestoreVolume(root, projectID, volumeID string) (string, error) {
	volume, err := ensureVolume(root, projectID, volumeID)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(volume)
	if err != nil {
		return "", fmt.Errorf("inspect managed PostgreSQL restore volume: %w", err)
	}
	if len(entries) != 0 {
		return "", errors.New("managed PostgreSQL restore volume is not empty")
	}
	return volume, nil
}

func (controller *Controller) removeManagedPostgresVolume(ctx context.Context, projectID, volumeID string) error {
	if !safeRoot(controller.volumeRoot) || !safePathComponent(projectID) || !safePathComponent(volumeID) {
		return errors.New("managed PostgreSQL volume removal input is invalid")
	}
	if err := controller.engine.RemoveManagedVolume(ctx, volumeID); err != nil {
		return fmt.Errorf("remove managed PostgreSQL runtime volume: %w", err)
	}
	projectRoot := filepath.Join(controller.volumeRoot, projectID)
	if err := os.RemoveAll(filepath.Join(projectRoot, volumeID)); err != nil {
		return err
	}
	if _, err := os.Stat(projectRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return syncPostgresDirectory(projectRoot)
}

func syncPostgresDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

type postgresContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader *postgresContextReader) Read(output []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(output)
}
