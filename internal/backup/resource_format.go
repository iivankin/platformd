package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/backupcrypto"
	"github.com/iivankin/platformd/internal/cryptobox"
)

const maximumResourceEnvelopeSize = 1 << 20

type ResourceEnvelope struct {
	FormatVersion   int                  `json:"formatVersion"`
	ResourceKind    string               `json:"resourceKind"`
	ResourceID      string               `json:"resourceId"`
	GenerationID    string               `json:"generationId"`
	CreatedAtMillis int64                `json:"createdAt"`
	PlaintextSize   int64                `json:"plaintextSize"`
	Chunks          []backupcrypto.Chunk `json:"chunks"`
}

type ResourceCompletion struct {
	FormatVersion     int    `json:"formatVersion"`
	ResourceKind      string `json:"resourceKind"`
	ResourceID        string `json:"resourceId"`
	GenerationID      string `json:"generationId"`
	EnvelopeSHA256    string `json:"envelopeSha256"`
	PlaintextSize     int64  `json:"plaintextSize"`
	RemoteSize        int64  `json:"remoteSize"`
	CompletedAtMillis int64  `json:"completedAt"`
}

type ResourceBuildConfig struct {
	Master       cryptobox.MasterKey
	ResourceKind string
	ResourceID   string
	GenerationID string
	WorkRoot     string
	CreatedAt    time.Time
	Random       io.Reader
}

type ResourceBuild struct {
	Envelope        ResourceEnvelope
	EnvelopeBytes   []byte
	Completion      ResourceCompletion
	CompletionBytes []byte
	Chunks          []backupcrypto.WorkChunk
	WorkDirectory   string
}

func BuildResource(ctx context.Context, config ResourceBuildConfig, input io.Reader) (ResourceBuild, error) {
	if !validBackupResourceKind(config.ResourceKind) || !validControlIdentifier(config.ResourceID) ||
		!validControlIdentifier(config.GenerationID) || !safeBackupRoot(config.WorkRoot) ||
		config.CreatedAt.IsZero() || input == nil {
		return ResourceBuild{}, errors.New("resource backup build configuration is incomplete")
	}
	if err := os.MkdirAll(config.WorkRoot, 0o700); err != nil {
		return ResourceBuild{}, err
	}
	workDirectory := filepath.Join(config.WorkRoot, "resource-"+config.ResourceKind+"-"+config.GenerationID)
	if err := os.Mkdir(workDirectory, 0o700); err != nil {
		return ResourceBuild{}, err
	}
	cleanup := func(err error) (ResourceBuild, error) {
		_ = os.RemoveAll(workDirectory)
		return ResourceBuild{}, err
	}
	cipher, err := backupcrypto.NewResourceCipher(config.Master, config.ResourceID)
	if err != nil {
		return cleanup(err)
	}
	writer, err := backupcrypto.NewWorkWriter(cipher, config.GenerationID, workDirectory, config.Random)
	if err != nil {
		return cleanup(err)
	}
	written, copyErr := io.Copy(writer, &contextReader{ctx: ctx, source: input})
	closeErr := writer.Close()
	if copyErr != nil || closeErr != nil || written <= 0 {
		return cleanup(errors.Join(copyErr, closeErr, errors.New("resource backup plaintext is empty")))
	}
	chunks, err := writer.Chunks()
	if err != nil {
		return cleanup(err)
	}
	descriptors := make([]backupcrypto.Chunk, len(chunks))
	remoteSize := int64(0)
	for index, chunk := range chunks {
		descriptors[index] = chunk.Chunk
		if int64(chunk.CiphertextSize) > math.MaxInt64-remoteSize {
			return cleanup(errors.New("resource backup remote size overflows"))
		}
		remoteSize += int64(chunk.CiphertextSize)
	}
	envelope := ResourceEnvelope{
		FormatVersion: ControlFormatVersion, ResourceKind: config.ResourceKind, ResourceID: config.ResourceID,
		GenerationID: config.GenerationID, CreatedAtMillis: config.CreatedAt.UnixMilli(),
		PlaintextSize: written, Chunks: descriptors,
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil || len(envelopeBytes) > maximumResourceEnvelopeSize {
		return cleanup(errors.Join(err, errors.New("resource backup envelope exceeds size limit")))
	}
	if int64(len(envelopeBytes)) > math.MaxInt64-remoteSize {
		return cleanup(errors.New("resource backup remote size overflows"))
	}
	// RemoteSize excludes complete.json because the marker contains RemoteSize
	// itself. It describes the immutable data that the marker commits.
	remoteSize += int64(len(envelopeBytes))
	envelopeHash := sha256.Sum256(envelopeBytes)
	completion := ResourceCompletion{
		FormatVersion: ControlFormatVersion, ResourceKind: config.ResourceKind, ResourceID: config.ResourceID,
		GenerationID: config.GenerationID, EnvelopeSHA256: hex.EncodeToString(envelopeHash[:]),
		PlaintextSize: written, RemoteSize: remoteSize, CompletedAtMillis: config.CreatedAt.UnixMilli(),
	}
	completionBytes, err := json.Marshal(completion)
	if err != nil {
		return cleanup(err)
	}
	return ResourceBuild{
		Envelope: envelope, EnvelopeBytes: envelopeBytes, Completion: completion, CompletionBytes: completionBytes,
		Chunks: chunks, WorkDirectory: workDirectory,
	}, nil
}

func DecodeResourceEnvelope(value []byte) (ResourceEnvelope, error) {
	if len(value) == 0 || len(value) > maximumResourceEnvelopeSize {
		return ResourceEnvelope{}, errors.New("resource envelope size is outside bounds")
	}
	if err := rejectDuplicateJSONKeys(value); err != nil {
		return ResourceEnvelope{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var envelope ResourceEnvelope
	if err := decoder.Decode(&envelope); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ResourceEnvelope{}, errors.New("resource envelope JSON is invalid")
	}
	if envelope.FormatVersion != ControlFormatVersion || !validBackupResourceKind(envelope.ResourceKind) ||
		!validControlIdentifier(envelope.ResourceID) || !validControlIdentifier(envelope.GenerationID) ||
		envelope.CreatedAtMillis <= 0 || envelope.PlaintextSize <= 0 || len(envelope.Chunks) == 0 {
		return ResourceEnvelope{}, errors.New("resource envelope fields are invalid")
	}
	total := int64(0)
	for index, chunk := range envelope.Chunks {
		if chunk.Index != index || chunk.PlaintextSize < 0 || chunk.PlaintextSize > backupcrypto.DefaultChunkSize ||
			chunk.CiphertextSize <= 0 || len(chunk.CiphertextSHA) != sha256.Size*2 ||
			strings.ToLower(chunk.CiphertextSHA) != chunk.CiphertextSHA {
			return ResourceEnvelope{}, errors.New("resource envelope chunk descriptors are invalid")
		}
		if _, err := hex.DecodeString(chunk.CiphertextSHA); err != nil {
			return ResourceEnvelope{}, errors.New("resource envelope chunk checksum is invalid")
		}
		if int64(chunk.PlaintextSize) > math.MaxInt64-total {
			return ResourceEnvelope{}, errors.New("resource envelope plaintext size overflows")
		}
		total += int64(chunk.PlaintextSize)
	}
	if total != envelope.PlaintextSize {
		return ResourceEnvelope{}, errors.New("resource envelope plaintext size differs from chunks")
	}
	return envelope, nil
}

func DecodeResourceCompletion(value []byte) (ResourceCompletion, error) {
	if len(value) == 0 || len(value) > 64<<10 {
		return ResourceCompletion{}, errors.New("resource completion size is outside bounds")
	}
	if err := rejectDuplicateJSONKeys(value); err != nil {
		return ResourceCompletion{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var completion ResourceCompletion
	if err := decoder.Decode(&completion); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ResourceCompletion{}, errors.New("resource completion JSON is invalid")
	}
	if err := validateResourceCompletion(completion); err != nil {
		return ResourceCompletion{}, err
	}
	return completion, nil
}

func validateResourceCompletion(completion ResourceCompletion) error {
	if completion.FormatVersion != ControlFormatVersion || !validBackupResourceKind(completion.ResourceKind) ||
		!validControlIdentifier(completion.ResourceID) || !validControlIdentifier(completion.GenerationID) ||
		completion.CompletedAtMillis <= 0 || completion.PlaintextSize <= 0 || completion.RemoteSize <= 0 ||
		len(completion.EnvelopeSHA256) != sha256.Size*2 || strings.ToLower(completion.EnvelopeSHA256) != completion.EnvelopeSHA256 {
		return errors.New("resource completion fields are invalid")
	}
	if _, err := hex.DecodeString(completion.EnvelopeSHA256); err != nil {
		return errors.New("resource completion envelope checksum is invalid")
	}
	return nil
}

func validBackupResourceKind(value string) bool {
	switch value {
	case "registry", "object_store", "postgres", "redis":
		return true
	default:
		return false
	}
}

func ResourceGenerationsPrefix(kind, resourceID string) string {
	return "resources/" + kind + "/" + resourceID + "/generations"
}

func ResourceGenerationPrefix(kind, resourceID, generationID string) string {
	return ResourceGenerationsPrefix(kind, resourceID) + "/" + generationID
}

func ResourceChunkKey(kind, resourceID, generationID string, index int) string {
	return fmt.Sprintf("%s/chunk-%08d.pdx", ResourceGenerationPrefix(kind, resourceID, generationID), index)
}

func ResourceEnvelopeKey(kind, resourceID, generationID string) string {
	return ResourceGenerationPrefix(kind, resourceID, generationID) + "/envelope.json"
}

func ResourceCompletionKey(kind, resourceID, generationID string) string {
	return ResourceGenerationPrefix(kind, resourceID, generationID) + "/complete.json"
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader *contextReader) Read(output []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(output)
}
