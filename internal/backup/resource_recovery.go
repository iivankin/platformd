package backup

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/iivankin/platformd/internal/cryptobox"
)

type LatestResourceRestore struct {
	Remote       ControlRemote
	Master       cryptobox.MasterKey
	ResourceKind string
	ResourceID   string
	Restorer     ResourceRestorer
	Options      ResourceRestoreOptions
	Actor        Actor
}

// RestoreLatestResource is the synchronous, queue-free primitive used during
// disaster recovery before the ordinary control plane starts. A missing
// generation is a successful "not found" result so the caller can initialize
// that resource empty according to its engine contract.
func RestoreLatestResource(
	ctx context.Context,
	input LatestResourceRestore,
) (ResourceCompletion, bool, error) {
	if ctx == nil || input.Remote == nil || !validBackupResourceKind(input.ResourceKind) ||
		!validControlIdentifier(input.ResourceID) || input.Restorer == nil {
		return ResourceCompletion{}, false, errors.New("latest resource restore input is invalid")
	}
	generations, err := ListResourceGenerations(ctx, input.Remote, input.ResourceKind, input.ResourceID)
	if err != nil {
		return ResourceCompletion{}, false, err
	}
	if len(generations) == 0 {
		return ResourceCompletion{}, false, nil
	}
	completion := generations[0]
	reader, err := OpenResource(ctx, input.Remote, input.Master, completion)
	if err != nil {
		return ResourceCompletion{}, false, err
	}
	counted := &countingResourceReader{source: reader}
	request := ResourceRestoreRequest{
		ResourceKind: input.ResourceKind, ResourceID: input.ResourceID,
		GenerationID: completion.GenerationID, Options: input.Options, Actor: input.Actor,
		Source: ResourceRestoreSource{
			Completion: completion, Envelope: reader.Envelope(), Reader: counted,
			OpenAttachment: func(attachment ResourceAttachment) (io.ReadCloser, error) {
				return OpenResourceAttachment(ctx, input.Remote, reader.Envelope(), attachment)
			},
		},
	}
	if err := input.Restorer.Restore(ctx, request); err != nil {
		return ResourceCompletion{}, false, err
	}
	if counted.read != reader.Envelope().PlaintextSize {
		return ResourceCompletion{}, false, fmt.Errorf(
			"%s recovery restorer did not consume the complete generation",
			input.ResourceKind,
		)
	}
	return completion, true, nil
}
