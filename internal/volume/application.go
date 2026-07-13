package volume

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/state"
)

var ErrInvalidInput = errors.New("invalid volume input")

type Repository interface {
	CreateVolume(context.Context, state.CreateVolume) (state.Volume, error)
	VolumesByService(context.Context, string, string) ([]state.Volume, error)
	DeleteVolume(context.Context, state.DeleteVolume) (state.Volume, error)
	Service(context.Context, string, string) (state.ServiceDesired, error)
}

type ImageInspector interface {
	InspectImage(context.Context, string) (containerengine.Image, error)
}

type Filesystem interface {
	Ensure(state.PersistentVolumeReference) error
	Remove(projectID, volumeID string) error
}

type Actor struct {
	Kind  string
	ID    string
	Email string
}

type CreateInput struct {
	ProjectID string
	ServiceID string
	Name      string
	OwnerUID  int
	OwnerGID  int
	Actor     Actor
}

type DeleteInput struct {
	ProjectID string
	ServiceID string
	VolumeID  string
	Actor     Actor
}

type MutationResult struct {
	Volume    state.Volume
	RequestID string
}

type OwnerSuggestion struct {
	OwnerUID     int
	OwnerGID     int
	ImageUser    string
	ExactNumeric bool
}

type Config struct {
	Repository     Repository
	Filesystem     Filesystem
	Images         ImageInspector
	Random         io.Reader
	Now            func() time.Time
	OnCleanupError func(error)
}

type Application struct {
	repository     Repository
	filesystem     Filesystem
	images         ImageInspector
	random         io.Reader
	now            func() time.Time
	onCleanupError func(error)
}

func New(config Config) (*Application, error) {
	if config.Repository == nil || config.Filesystem == nil || config.Images == nil {
		return nil, errors.New("volume application dependencies are incomplete")
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.OnCleanupError == nil {
		config.OnCleanupError = func(error) {}
	}
	return &Application{
		repository: config.Repository, filesystem: config.Filesystem, images: config.Images,
		random: config.Random, now: config.Now, onCleanupError: config.OnCleanupError,
	}, nil
}

func NewLocalFilesystem(root string) Filesystem {
	return localFilesystem{root: root}
}

func (application *Application) List(ctx context.Context, projectID, serviceID string) ([]state.Volume, error) {
	if projectID == "" || serviceID == "" {
		return nil, ErrInvalidInput
	}
	return application.repository.VolumesByService(ctx, projectID, serviceID)
}

func (application *Application) Create(ctx context.Context, input CreateInput) (MutationResult, error) {
	if err := validateCreate(input); err != nil {
		return MutationResult{}, err
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(timestamp, 3)
	if err != nil {
		return MutationResult{}, err
	}
	volume := state.Volume{
		ID: identifiers[0], ProjectID: input.ProjectID, ServiceID: input.ServiceID,
		Name: input.Name, OwnerUID: input.OwnerUID, OwnerGID: input.OwnerGID,
		CreatedAtMillis: timestamp.UnixMilli(),
	}
	reference := state.PersistentVolumeReference{
		ProjectID: input.ProjectID, VolumeID: volume.ID, Kind: state.PersistentVolumeOrdinary,
		OwnerUID: input.OwnerUID, OwnerGID: input.OwnerGID,
	}
	if err := application.filesystem.Ensure(reference); err != nil {
		return MutationResult{}, fmt.Errorf("create volume directory: %w", err)
	}
	created, err := application.repository.CreateVolume(ctx, state.CreateVolume{
		Volume: volume, AuditEventID: identifiers[1],
		ActorKind: input.Actor.Kind, ActorID: input.Actor.ID, ActorEmail: input.Actor.Email,
		RequestCorrelationID: identifiers[2],
	})
	if err != nil {
		cleanupErr := application.filesystem.Remove(input.ProjectID, volume.ID)
		return MutationResult{}, errors.Join(err, cleanupErr)
	}
	return MutationResult{Volume: created, RequestID: identifiers[2]}, nil
}

func (application *Application) Delete(ctx context.Context, input DeleteInput) (MutationResult, error) {
	if err := validateActorAndScope(input.ProjectID, input.ServiceID, input.Actor); err != nil || input.VolumeID == "" {
		return MutationResult{}, ErrInvalidInput
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(timestamp, 2)
	if err != nil {
		return MutationResult{}, err
	}
	deleted, err := application.repository.DeleteVolume(ctx, state.DeleteVolume{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, VolumeID: input.VolumeID,
		AuditEventID: identifiers[0], ActorKind: input.Actor.Kind, ActorID: input.Actor.ID,
		ActorEmail: input.Actor.Email, RequestCorrelationID: identifiers[1],
		DeletedAtMillis: timestamp.UnixMilli(),
	})
	if err != nil {
		return MutationResult{}, err
	}
	cleanupErr := application.filesystem.Remove(deleted.ProjectID, deleted.ID)
	if cleanupErr != nil {
		application.onCleanupError(fmt.Errorf("remove deleted volume %s/%s: %w", deleted.ProjectID, deleted.ID, cleanupErr))
	}
	return MutationResult{Volume: deleted, RequestID: identifiers[1]}, nil
}

func (application *Application) SuggestOwner(ctx context.Context, projectID, serviceID string) (OwnerSuggestion, error) {
	if projectID == "" || serviceID == "" {
		return OwnerSuggestion{}, ErrInvalidInput
	}
	service, err := application.repository.Service(ctx, projectID, serviceID)
	if err != nil {
		return OwnerSuggestion{}, err
	}
	if service.ActiveImageDigest == "" {
		return OwnerSuggestion{}, nil
	}
	image, err := application.images.InspectImage(ctx, service.ActiveImageDigest)
	if err != nil {
		return OwnerSuggestion{}, fmt.Errorf("inspect active service image: %w", err)
	}
	uid, gid, exact := parseNumericImageUser(image.User)
	return OwnerSuggestion{OwnerUID: uid, OwnerGID: gid, ImageUser: image.User, ExactNumeric: exact}, nil
}

func validateCreate(input CreateInput) error {
	if err := validateActorAndScope(input.ProjectID, input.ServiceID, input.Actor); err != nil {
		return err
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if input.OwnerUID < 0 || uint64(input.OwnerUID) > maximumOwnerID || input.OwnerGID < 0 || uint64(input.OwnerGID) > maximumOwnerID {
		return fmt.Errorf("%w: volume owner IDs must be between 0 and %d", ErrInvalidInput, maximumOwnerID)
	}
	return nil
}

func validateActorAndScope(projectID, serviceID string, actor Actor) error {
	if projectID == "" || serviceID == "" || actor.ID == "" ||
		(actor.Kind != "access" && actor.Kind != "token") || (actor.Kind == "access" && actor.Email == "") {
		return ErrInvalidInput
	}
	return nil
}

func (application *Application) identifiers(timestamp time.Time, count int) ([]string, error) {
	if timestamp.UnixMilli() <= 0 {
		return nil, errors.New("volume mutation timestamp is invalid")
	}
	values := make([]string, count)
	for index := range values {
		value, err := id.NewWith(timestamp, application.random)
		if err != nil {
			return nil, fmt.Errorf("allocate volume mutation identifiers: %w", err)
		}
		values[index] = value
	}
	return values, nil
}
