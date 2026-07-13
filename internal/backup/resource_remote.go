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
	"sort"
	"strings"

	"github.com/iivankin/platformd/internal/backupcrypto"
	"github.com/iivankin/platformd/internal/cryptobox"
)

func PublishResource(ctx context.Context, remote ControlRemote, master cryptobox.MasterKey, build ResourceBuild) error {
	if remote == nil || build.Envelope.ResourceKind == "" || build.Envelope.ResourceID == "" ||
		build.Envelope.GenerationID == "" || len(build.Chunks) == 0 ||
		len(build.Chunks) != len(build.Envelope.Chunks) || len(build.EnvelopeBytes) == 0 || len(build.CompletionBytes) == 0 {
		return errors.New("resource publication input is incomplete")
	}
	for _, chunk := range build.Chunks {
		file, err := os.Open(chunk.Path)
		if err != nil {
			return err
		}
		key := remote.Key(ResourceChunkKey(build.Envelope.ResourceKind, build.Envelope.ResourceID, build.Envelope.GenerationID, chunk.Index))
		putErr := remote.Put(ctx, key, file, int64(chunk.CiphertextSize), chunk.CiphertextSHA)
		closeErr := file.Close()
		if putErr != nil || closeErr != nil {
			return errors.Join(putErr, closeErr)
		}
	}
	envelopeHash := sha256.Sum256(build.EnvelopeBytes)
	envelopeKey := remote.Key(ResourceEnvelopeKey(build.Envelope.ResourceKind, build.Envelope.ResourceID, build.Envelope.GenerationID))
	if err := remote.Put(ctx, envelopeKey, bytes.NewReader(build.EnvelopeBytes), int64(len(build.EnvelopeBytes)), hex.EncodeToString(envelopeHash[:])); err != nil {
		return err
	}
	if err := verifyPublishedResource(ctx, remote, master, build.Envelope, build.EnvelopeBytes); err != nil {
		return err
	}
	completionHash := sha256.Sum256(build.CompletionBytes)
	completionKey := remote.Key(ResourceCompletionKey(build.Envelope.ResourceKind, build.Envelope.ResourceID, build.Envelope.GenerationID))
	if err := remote.Put(ctx, completionKey, bytes.NewReader(build.CompletionBytes), int64(len(build.CompletionBytes)), hex.EncodeToString(completionHash[:])); err != nil {
		return err
	}
	published, err := readRemoteObject(ctx, remote, completionKey, 64<<10)
	if err != nil || !bytes.Equal(published, build.CompletionBytes) {
		return errors.Join(err, errors.New("resource completion read-back differs"))
	}
	_, err = DecodeResourceCompletion(published)
	return err
}

func verifyPublishedResource(ctx context.Context, remote ControlRemote, master cryptobox.MasterKey, envelope ResourceEnvelope, expected []byte) error {
	value, err := readRemoteObject(
		ctx, remote, remote.Key(ResourceEnvelopeKey(envelope.ResourceKind, envelope.ResourceID, envelope.GenerationID)), maximumResourceEnvelopeSize,
	)
	if err != nil || !bytes.Equal(value, expected) {
		return errors.Join(err, errors.New("resource envelope read-back differs"))
	}
	decoded, err := DecodeResourceEnvelope(value)
	if err != nil || decoded.ResourceKind != envelope.ResourceKind || decoded.ResourceID != envelope.ResourceID ||
		decoded.GenerationID != envelope.GenerationID {
		return errors.Join(err, errors.New("resource envelope identity differs"))
	}
	cipher, err := backupcrypto.NewResourceCipher(master, decoded.ResourceID)
	if err != nil {
		return err
	}
	for _, chunk := range decoded.Chunks {
		sealed, err := readRemoteObject(ctx, remote, remote.Key(ResourceChunkKey(
			decoded.ResourceKind, decoded.ResourceID, decoded.GenerationID, chunk.Index,
		)), int64(chunk.CiphertextSize))
		if err != nil {
			return err
		}
		plaintext, err := cipher.OpenChunk(decoded.GenerationID, chunk, sealed)
		clear(sealed)
		if err != nil {
			return fmt.Errorf("verify resource chunk %d: %w", chunk.Index, err)
		}
		clear(plaintext)
	}
	return nil
}

func ListResourceGenerations(ctx context.Context, remote ControlRemote, kind, resourceID string) ([]ResourceCompletion, error) {
	if remote == nil || !validBackupResourceKind(kind) || !validControlIdentifier(resourceID) {
		return nil, errors.New("resource generation list input is invalid")
	}
	prefix := remote.Key(ResourceGenerationsPrefix(kind, resourceID) + "/")
	continuation := ""
	completionKeys := make([]string, 0)
	for {
		page, err := remote.List(ctx, prefix, continuation)
		if err != nil {
			return nil, err
		}
		for _, object := range page.Objects {
			if strings.HasPrefix(object.Key, prefix) && strings.HasSuffix(object.Key, "/complete.json") {
				completionKeys = append(completionKeys, object.Key)
			}
		}
		if page.Continuation == "" {
			break
		}
		continuation = page.Continuation
	}
	sort.Strings(completionKeys)
	result := make([]ResourceCompletion, 0, len(completionKeys))
	seen := make(map[string]struct{}, len(completionKeys))
	for _, key := range completionKeys {
		value, err := readRemoteObject(ctx, remote, key, 64<<10)
		if err != nil {
			return nil, err
		}
		completion, err := DecodeResourceCompletion(value)
		if err != nil || completion.ResourceKind != kind || completion.ResourceID != resourceID ||
			key != remote.Key(ResourceCompletionKey(kind, resourceID, completion.GenerationID)) {
			return nil, errors.Join(err, errors.New("resource completion identity differs from key"))
		}
		if _, exists := seen[completion.GenerationID]; exists {
			return nil, errors.New("resource generation list contains duplicate completion")
		}
		seen[completion.GenerationID] = struct{}{}
		result = append(result, completion)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].CompletedAtMillis == result[right].CompletedAtMillis {
			return result[left].GenerationID > result[right].GenerationID
		}
		return result[left].CompletedAtMillis > result[right].CompletedAtMillis
	})
	return result, nil
}

func ApplyResourceRetention(ctx context.Context, remote ControlRemote, kind, resourceID string, retain int) error {
	if retain < 1 || retain > 100 {
		return errors.New("resource retention count must be 1..100")
	}
	generations, err := ListResourceGenerations(ctx, remote, kind, resourceID)
	if err != nil {
		return err
	}
	for _, generation := range generations[min(retain, len(generations)):] {
		prefix := remote.Key(ResourceGenerationPrefix(kind, resourceID, generation.GenerationID) + "/")
		if err := deleteRemotePrefix(ctx, remote, prefix); err != nil {
			return err
		}
	}
	return nil
}

type ResourceReader struct {
	ctx      context.Context
	remote   ControlRemote
	cipher   *backupcrypto.ResourceCipher
	envelope ResourceEnvelope
	index    int
	current  []byte
	offset   int
}

func OpenResource(ctx context.Context, remote ControlRemote, master cryptobox.MasterKey, completion ResourceCompletion) (*ResourceReader, error) {
	if remote == nil {
		return nil, errors.New("resource remote is nil")
	}
	if err := validateResourceCompletion(completion); err != nil {
		return nil, err
	}
	value, err := readRemoteObject(ctx, remote, remote.Key(ResourceEnvelopeKey(
		completion.ResourceKind, completion.ResourceID, completion.GenerationID,
	)), maximumResourceEnvelopeSize)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(value)
	if hex.EncodeToString(hash[:]) != completion.EnvelopeSHA256 {
		return nil, errors.New("resource envelope checksum differs from completion")
	}
	envelope, err := DecodeResourceEnvelope(value)
	if err != nil || envelope.ResourceKind != completion.ResourceKind || envelope.ResourceID != completion.ResourceID ||
		envelope.GenerationID != completion.GenerationID || envelope.PlaintextSize != completion.PlaintextSize {
		return nil, errors.Join(err, errors.New("resource envelope differs from completion"))
	}
	cipher, err := backupcrypto.NewResourceCipher(master, completion.ResourceID)
	if err != nil {
		return nil, err
	}
	return &ResourceReader{ctx: ctx, remote: remote, cipher: cipher, envelope: envelope}, nil
}

func (reader *ResourceReader) Read(output []byte) (int, error) {
	if len(output) == 0 {
		return 0, nil
	}
	for reader.offset == len(reader.current) {
		clear(reader.current)
		reader.current = nil
		reader.offset = 0
		if reader.index == len(reader.envelope.Chunks) {
			return 0, io.EOF
		}
		chunk := reader.envelope.Chunks[reader.index]
		sealed, err := readRemoteObject(reader.ctx, reader.remote, reader.remote.Key(ResourceChunkKey(
			reader.envelope.ResourceKind, reader.envelope.ResourceID, reader.envelope.GenerationID, chunk.Index,
		)), int64(chunk.CiphertextSize))
		if err != nil {
			return 0, err
		}
		plaintext, err := reader.cipher.OpenChunk(reader.envelope.GenerationID, chunk, sealed)
		clear(sealed)
		if err != nil {
			return 0, err
		}
		reader.current = plaintext
		reader.index++
	}
	count := copy(output, reader.current[reader.offset:])
	reader.offset += count
	return count, nil
}
