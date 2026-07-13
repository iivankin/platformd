package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/iivankin/platformd/internal/backupcrypto"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/remotes3"
)

type ControlRemote interface {
	Key(string) string
	Put(context.Context, string, io.Reader, int64, string) error
	Get(context.Context, string) (io.ReadCloser, int64, error)
	Delete(context.Context, string) error
	List(context.Context, string, string) (remotes3.Page, error)
}

type PublishedControlError struct{ Err error }

func (failure *PublishedControlError) Error() string {
	return "control generation was published but previous-generation cleanup failed: " + failure.Err.Error()
}

func (failure *PublishedControlError) Unwrap() error { return failure.Err }

func PublishControl(ctx context.Context, remote ControlRemote, master cryptobox.MasterKey, build ControlBuild) error {
	if remote == nil || build.Envelope.InstallationID == "" || build.Envelope.GenerationID == "" ||
		len(build.Chunks) == 0 || len(build.Chunks) != len(build.Envelope.Chunks) || len(build.EnvelopeBytes) == 0 ||
		len(build.CompletionBytes) == 0 {
		return errors.New("control publication input is incomplete")
	}
	previous, previousExists, err := currentControlCompletion(ctx, remote)
	if err != nil {
		return err
	}
	for _, chunk := range build.Chunks {
		file, err := os.Open(chunk.Path)
		if err != nil {
			return err
		}
		key := remote.Key(ControlChunkKey(build.Envelope.GenerationID, chunk.Index))
		putErr := remote.Put(ctx, key, file, int64(chunk.CiphertextSize), chunk.CiphertextSHA)
		closeErr := file.Close()
		if putErr != nil || closeErr != nil {
			return errors.Join(putErr, closeErr)
		}
	}
	envelopeHash := sha256.Sum256(build.EnvelopeBytes)
	if err := remote.Put(
		ctx, remote.Key(ControlEnvelopeKey(build.Envelope.GenerationID)), bytes.NewReader(build.EnvelopeBytes),
		int64(len(build.EnvelopeBytes)), hex.EncodeToString(envelopeHash[:]),
	); err != nil {
		return err
	}
	if err := verifyPublishedControl(ctx, remote, master, build.Envelope, build.EnvelopeBytes); err != nil {
		return err
	}
	completionHash := sha256.Sum256(build.CompletionBytes)
	completionKey := remote.Key(ControlCompletionKey())
	if err := remote.Put(ctx, completionKey, bytes.NewReader(build.CompletionBytes), int64(len(build.CompletionBytes)), hex.EncodeToString(completionHash[:])); err != nil {
		return err
	}
	published, err := readRemoteObject(ctx, remote, completionKey, 64<<10)
	if err != nil || !bytes.Equal(published, build.CompletionBytes) {
		return errors.Join(err, errors.New("control completion read-back differs"))
	}
	if _, err := DecodeControlCompletion(published); err != nil {
		return err
	}
	if previousExists && previous.GenerationID != build.Completion.GenerationID {
		if err := deleteRemotePrefix(ctx, remote, remote.Key(ControlGenerationPrefix(previous.GenerationID)+"/")); err != nil {
			return &PublishedControlError{Err: fmt.Errorf("delete previous control generation: %w", err)}
		}
	}
	return nil
}

func verifyPublishedControl(ctx context.Context, remote ControlRemote, master cryptobox.MasterKey, envelope ControlEnvelope, expectedEnvelope []byte) error {
	value, err := readRemoteObject(ctx, remote, remote.Key(ControlEnvelopeKey(envelope.GenerationID)), maximumEnvelopeSize)
	if err != nil || !bytes.Equal(value, expectedEnvelope) {
		return errors.Join(err, errors.New("control envelope read-back differs"))
	}
	decoded, err := DecodeControlEnvelope(value)
	if err != nil || decoded.InstallationID != envelope.InstallationID || decoded.GenerationID != envelope.GenerationID {
		return errors.Join(err, errors.New("control envelope identity differs"))
	}
	cipher, err := backupcrypto.NewControlCipher(master, envelope.InstallationID)
	if err != nil {
		return err
	}
	for _, chunk := range decoded.Chunks {
		sealed, err := readRemoteObject(
			ctx, remote, remote.Key(ControlChunkKey(decoded.GenerationID, chunk.Index)), int64(chunk.CiphertextSize),
		)
		if err != nil {
			return err
		}
		plaintext, err := cipher.OpenChunk(decoded.GenerationID, chunk, sealed)
		clear(sealed)
		if err != nil {
			return fmt.Errorf("verify control chunk %d: %w", chunk.Index, err)
		}
		clear(plaintext)
	}
	return nil
}

func currentControlCompletion(ctx context.Context, remote ControlRemote) (ControlCompletion, bool, error) {
	value, err := readRemoteObject(ctx, remote, remote.Key(ControlCompletionKey()), 64<<10)
	if err != nil {
		var remoteErr *remotes3.RemoteError
		if errors.As(err, &remoteErr) && remoteErr.StatusCode == 404 {
			return ControlCompletion{}, false, nil
		}
		return ControlCompletion{}, false, err
	}
	completion, err := DecodeControlCompletion(value)
	return completion, err == nil, err
}

func readRemoteObject(ctx context.Context, remote ControlRemote, key string, maximum int64) ([]byte, error) {
	body, size, err := remote.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if size < 0 || size > maximum {
		return nil, errors.Join(errors.New("remote control object size is outside bounds"), body.Close())
	}
	value, readErr := io.ReadAll(io.LimitReader(body, maximum+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if int64(len(value)) != size {
		return nil, errors.New("remote control object content length differs")
	}
	return value, nil
}

func deleteRemotePrefix(ctx context.Context, remote ControlRemote, prefix string) error {
	continuation := ""
	for {
		page, err := remote.List(ctx, prefix, continuation)
		if err != nil {
			return err
		}
		for _, object := range page.Objects {
			if err := remote.Delete(ctx, object.Key); err != nil {
				return err
			}
		}
		if page.Continuation == "" {
			return nil
		}
		continuation = page.Continuation
	}
}
