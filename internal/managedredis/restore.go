package managedredis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
)

// RestoreReplace loads a backup into an unpublished volume and switches the
// single durable volume pointer only after the candidate Redis accepts AUTH and
// PING. No durable maintenance or candidate state is needed: startup orphan
// cleanup can remove every volume except the one referenced by SQLite.
func (controller *Controller) RestoreReplace(
	ctx context.Context,
	resourceID string,
	rdb io.Reader,
	actor Actor,
) (resultErr error) {
	if ctx == nil || !safePathComponent(resourceID) || rdb == nil {
		return errors.New("managed Redis restore input is invalid")
	}
	if !validRestoreActor(actor) {
		return errors.New("managed Redis restore actor is invalid")
	}
	lock := controller.resourceLock(resourceID)
	lock.Lock()
	defer lock.Unlock()

	resource, err := controller.store.ManagedRedis(ctx, resourceID)
	if err != nil {
		return err
	}
	oldRuntime, oldRunning := controller.activeRuntime(resourceID)
	if oldRunning && oldRuntime.resource.VolumeID != resource.VolumeID {
		return errors.New("managed Redis runtime does not match the active volume")
	}
	password, err := controller.password(resource)
	if err != nil {
		return fmt.Errorf("open managed Redis password: %w", err)
	}
	if !validPassword(password) {
		return errors.New("managed Redis password has an invalid generated format")
	}
	placement, err := controller.placement(resource)
	if err != nil {
		return fmt.Errorf("place managed Redis restore candidate: %w", err)
	}
	image, err := controller.resolveImage(ctx, resource)
	if err != nil {
		return err
	}
	if err := controller.growth.PermitGrowth(ctx); err != nil {
		return fmt.Errorf("create managed Redis restore volume: %w", err)
	}

	timestamp := controller.now()
	identifiers, err := controller.restoreIdentifiers(timestamp)
	if err != nil {
		return err
	}
	volumeID, runtimeID, auditID, correlationID := identifiers[0], identifiers[1], identifiers[2], identifiers[3]
	if volumeID == resource.VolumeID || runtimeID == resource.ID || volumeID == runtimeID {
		return errors.New("managed Redis restore identifiers are not unique")
	}
	if err := controller.beginCandidateDeployment(ctx, runtimeID, resource, timestamp.UnixMilli()); err != nil {
		return err
	}
	defer controller.finishCandidateDeployment(ctx, runtimeID, &resultErr)
	volume, err := createRestoreVolume(controller.volumeRoot, resource.ProjectID, volumeID)
	if err != nil {
		return err
	}
	candidateContainer := containerengine.Container{}
	candidateStarted := false
	committed := false
	defer func() {
		if committed {
			return
		}
		resultErr = errors.Join(resultErr, controller.removeRestoreCandidate(
			candidateContainer.ID, candidateStarted, resource.ProjectID, volumeID, runtimeID,
		))
	}()
	if err := writeRestoreRDB(ctx, volume, rdb); err != nil {
		return err
	}
	configPath, err := writeConfig(controller.generatedRoot, runtimeID, password)
	if err != nil {
		return err
	}
	candidateContainer, err = controller.createContainerAttempt(
		ctx, resource, runtimeID, image.ID, placement, volume, configPath,
	)
	if err != nil {
		return err
	}
	if err := controller.engine.StartContainer(ctx, candidateContainer.ID); err != nil {
		return fmt.Errorf("start managed Redis restore candidate: %w", err)
	}
	candidateStarted = true
	ready, err := controller.waitReady(ctx, candidateContainer.ID, placement.NetworkName, password)
	if err != nil {
		return fmt.Errorf("validate managed Redis restore candidate: %w", err)
	}
	candidateContainer = ready

	oldStopped := false
	if oldRunning {
		if err := controller.publisher.WithdrawRedis(oldRuntime.resource); err != nil {
			return fmt.Errorf("withdraw managed Redis before restore switch: %w", err)
		}
		if err := controller.engine.StopContainer(oldRuntime.container.ID, stopTimeoutSeconds); err != nil {
			return errors.Join(
				fmt.Errorf("stop managed Redis before restore switch: %w", err),
				controller.publisher.PublishRedis(oldRuntime.resource, oldRuntime.container),
			)
		}
		oldStopped = true
	}
	err = controller.store.SwitchManagedRedisVolume(ctx, state.SwitchManagedRedisVolume{
		ResourceID: resource.ID, ExpectedVolumeID: resource.VolumeID, VolumeID: volumeID,
		Action: "redis.restore", AuditEventID: auditID,
		ActorKind: actor.Kind, ActorID: actor.ID, ActorEmail: actor.Email,
		RequestCorrelationID: correlationID, UpdatedAtMillis: timestamp.UnixMilli(),
	})
	if err != nil {
		return errors.Join(err, controller.recoverOldRuntime(oldRuntime, oldRunning && oldStopped, password))
	}
	committed = true
	switched := resource
	switched.VolumeID = volumeID
	switched.UpdatedAtMillis = timestamp.UnixMilli()
	controller.setActive(resourceID, activeRuntime{
		resource: switched, container: candidateContainer, network: placement.NetworkName, runtimeID: runtimeID,
	})
	if err := controller.activateCandidateDeployment(ctx, resourceID, runtimeID); err != nil {
		return err
	}
	publishErr := controller.publisher.PublishRedis(switched, candidateContainer)
	cleanupErr := controller.removeReplacedRuntime(ctx, oldRuntime, oldRunning, resource)
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

func (controller *Controller) restoreIdentifiers(timestamp time.Time) ([4]string, error) {
	var identifiers [4]string
	seen := make(map[string]struct{}, len(identifiers))
	for index := range identifiers {
		identifier, err := controller.newID(timestamp)
		if err != nil {
			return [4]string{}, fmt.Errorf("allocate managed Redis restore ID: %w", err)
		}
		if !safePathComponent(identifier) {
			return [4]string{}, errors.New("managed Redis restore ID source returned an invalid ID")
		}
		if _, duplicate := seen[identifier]; duplicate {
			return [4]string{}, errors.New("managed Redis restore ID source returned duplicate IDs")
		}
		seen[identifier] = struct{}{}
		identifiers[index] = identifier
	}
	return identifiers, nil
}

func (controller *Controller) recoverOldRuntime(
	runtime activeRuntime,
	stopped bool,
	password string,
) error {
	if !stopped {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), controller.readyTimeout+30*time.Second)
	defer cancel()
	if err := controller.engine.StartContainer(ctx, runtime.container.ID); err != nil {
		return fmt.Errorf("restart managed Redis after failed restore switch: %w", err)
	}
	ready, err := controller.waitReady(ctx, runtime.container.ID, runtime.network, password)
	if err != nil {
		return fmt.Errorf("validate managed Redis after failed restore switch: %w", err)
	}
	runtime.container = ready
	controller.setActive(runtime.resource.ID, runtime)
	if err := controller.publisher.PublishRedis(runtime.resource, ready); err != nil {
		return fmt.Errorf("republish managed Redis after failed restore switch: %w", err)
	}
	return nil
}

func (controller *Controller) removeRestoreCandidate(
	containerID string,
	started bool,
	projectID string,
	volumeID string,
	runtimeID string,
) error {
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
		volumeErr = controller.removeManagedRedisVolume(context.Background(), projectID, volumeID)
	}
	configErr := removeManagedRedisConfig(controller.generatedRoot, runtimeID)
	return errors.Join(stopErr, removeErr, volumeErr, configErr)
}

func (controller *Controller) removeReplacedRuntime(
	ctx context.Context,
	runtime activeRuntime,
	running bool,
	resource state.ManagedRedis,
) error {
	if running {
		if err := controller.engine.RemoveContainer(ctx, runtime.container.ID, true); err != nil {
			return fmt.Errorf("remove replaced managed Redis container: %w", err)
		}
	}
	configID := runtime.runtimeID
	if configID == "" {
		configID = resource.ID
	}
	return errors.Join(
		controller.removeManagedRedisVolume(ctx, resource.ProjectID, resource.VolumeID),
		removeManagedRedisConfig(controller.generatedRoot, configID),
	)
}

func (controller *Controller) removeManagedRedisVolume(ctx context.Context, projectID, volumeID string) error {
	if !safeRoot(controller.volumeRoot) || !safePathComponent(projectID) || !safePathComponent(volumeID) {
		return errors.New("managed Redis volume removal input is invalid")
	}
	if err := controller.engine.RemoveManagedVolume(ctx, volumeID); err != nil {
		return fmt.Errorf("remove managed Redis runtime volume: %w", err)
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
	return syncRedisDirectory(projectRoot)
}

func removeManagedRedisConfig(root, runtimeID string) error {
	if !safeRoot(root) || !safePathComponent(runtimeID) {
		return errors.New("managed Redis config removal input is invalid")
	}
	return os.RemoveAll(filepath.Join(root, runtimeID))
}

func createRestoreVolume(root, projectID, volumeID string) (string, error) {
	volume, err := ensureVolume(root, projectID, volumeID)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(volume)
	if err != nil {
		return "", fmt.Errorf("inspect managed Redis restore volume: %w", err)
	}
	if len(entries) != 0 {
		return "", errors.New("managed Redis restore volume is not empty")
	}
	return volume, nil
}

func writeRestoreRDB(ctx context.Context, volume string, source io.Reader) error {
	temporary, err := os.CreateTemp(volume, ".dump.rdb-")
	if err != nil {
		return fmt.Errorf("create managed Redis restore RDB: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	written, err := io.Copy(temporary, contextReader{ctx: ctx, source: source})
	if err != nil {
		return fmt.Errorf("write managed Redis restore RDB: %w", err)
	}
	if written == 0 {
		return errors.New("managed Redis restore RDB is empty")
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync managed Redis restore RDB: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close managed Redis restore RDB: %w", err)
	}
	destination := filepath.Join(volume, "dump.rdb")
	if _, err := os.Lstat(destination); err == nil {
		return errors.New("managed Redis restore RDB already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("publish managed Redis restore RDB: %w", err)
	}
	committed = true
	return syncRedisDirectory(volume)
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader contextReader) Read(output []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(output)
}

func syncRedisDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
