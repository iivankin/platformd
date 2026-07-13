package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
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
	AttachmentCount int                  `json:"attachmentCount"`
	AttachmentSize  int64                `json:"attachmentSize"`
	AttachmentRoot  string               `json:"attachmentRootSha256,omitempty"`
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
	Master          cryptobox.MasterKey
	ResourceKind    string
	ResourceID      string
	GenerationID    string
	WorkRoot        string
	CreatedAt       time.Time
	Random          io.Reader
	AttachmentPaths []string
}

type ResourceAttachment struct {
	Index  int
	Size   int64
	SHA256 string
}

type WorkResourceAttachment struct {
	ResourceAttachment
	Path string
}

type ResourceBuild struct {
	Envelope        ResourceEnvelope
	EnvelopeBytes   []byte
	Completion      ResourceCompletion
	CompletionBytes []byte
	Chunks          []backupcrypto.WorkChunk
	Attachments     []WorkResourceAttachment
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
	attachments, attachmentSize, attachmentRoot, err := buildResourceAttachments(ctx, config.AttachmentPaths)
	if err != nil {
		return cleanup(err)
	}
	if attachmentSize > math.MaxInt64-remoteSize {
		return cleanup(errors.New("resource backup remote size overflows"))
	}
	remoteSize += attachmentSize
	envelope := ResourceEnvelope{
		FormatVersion: ControlFormatVersion, ResourceKind: config.ResourceKind, ResourceID: config.ResourceID,
		GenerationID: config.GenerationID, CreatedAtMillis: config.CreatedAt.UnixMilli(),
		PlaintextSize: written, Chunks: descriptors, AttachmentCount: len(attachments),
		AttachmentSize: attachmentSize, AttachmentRoot: attachmentRoot,
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
		Chunks: chunks, Attachments: attachments, WorkDirectory: workDirectory,
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
		envelope.CreatedAtMillis <= 0 || envelope.PlaintextSize <= 0 || len(envelope.Chunks) == 0 ||
		envelope.AttachmentCount < 0 || envelope.AttachmentSize < 0 ||
		(envelope.AttachmentCount == 0 && (envelope.AttachmentSize != 0 || envelope.AttachmentRoot != "")) ||
		(envelope.AttachmentCount > 0 && (envelope.AttachmentSize <= 0 || !validSHA256(envelope.AttachmentRoot))) {
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

func buildResourceAttachments(ctx context.Context, paths []string) ([]WorkResourceAttachment, int64, string, error) {
	if len(paths) == 0 {
		return nil, 0, "", nil
	}
	result := make([]WorkResourceAttachment, 0, len(paths))
	total := int64(0)
	root := sha256.New()
	_, _ = io.WriteString(root, "platformd/resource-attachments/v1\x00")
	for index, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
			return nil, 0, "", errors.New("resource attachment path is invalid")
		}
		pathInfo, err := os.Lstat(path)
		if err != nil || !pathInfo.Mode().IsRegular() {
			return nil, 0, "", errors.Join(err, errors.New("resource attachment path is not a regular file"))
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, 0, "", err
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || !os.SameFile(pathInfo, info) || info.Size() <= 0 {
			_ = file.Close()
			return nil, 0, "", errors.Join(statErr, errors.New("resource attachment is empty or not a regular file"))
		}
		hash := sha256.New()
		written, copyErr := io.Copy(hash, &contextReader{ctx: ctx, source: file})
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || written != info.Size() {
			return nil, 0, "", errors.Join(copyErr, closeErr, errors.New("resource attachment size changed while hashing"))
		}
		if info.Size() > math.MaxInt64-total {
			return nil, 0, "", errors.New("resource attachment total size overflows")
		}
		total += info.Size()
		checksum := hex.EncodeToString(hash.Sum(nil))
		attachment := WorkResourceAttachment{
			ResourceAttachment: ResourceAttachment{Index: index, Size: info.Size(), SHA256: checksum}, Path: path,
		}
		result = append(result, attachment)
		writeAttachmentCommitment(root, attachment.ResourceAttachment)
	}
	return result, total, hex.EncodeToString(root.Sum(nil)), nil
}

func ValidateResourceAttachments(envelope ResourceEnvelope, attachments []ResourceAttachment) error {
	if envelope.AttachmentCount != len(attachments) {
		return errors.New("resource attachment count differs from envelope")
	}
	if len(attachments) == 0 {
		if envelope.AttachmentSize != 0 || envelope.AttachmentRoot != "" {
			return errors.New("empty resource attachments differ from envelope")
		}
		return nil
	}
	root := sha256.New()
	_, _ = io.WriteString(root, "platformd/resource-attachments/v1\x00")
	total := int64(0)
	for index, attachment := range attachments {
		if attachment.Index != index || attachment.Size <= 0 || !validSHA256(attachment.SHA256) ||
			attachment.Size > math.MaxInt64-total {
			return errors.New("resource attachment descriptor is invalid")
		}
		total += attachment.Size
		writeAttachmentCommitment(root, attachment)
	}
	if total != envelope.AttachmentSize || hex.EncodeToString(root.Sum(nil)) != envelope.AttachmentRoot {
		return errors.New("resource attachment commitment differs from envelope")
	}
	return nil
}

func writeAttachmentCommitment(writer io.Writer, attachment ResourceAttachment) {
	var numbers [16]byte
	binary.BigEndian.PutUint64(numbers[:8], uint64(attachment.Index))
	binary.BigEndian.PutUint64(numbers[8:], uint64(attachment.Size))
	_, _ = writer.Write(numbers[:])
	checksum, _ := hex.DecodeString(attachment.SHA256)
	_, _ = writer.Write(checksum)
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
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

func ResourceAttachmentKey(kind, resourceID, generationID string, index int) string {
	return fmt.Sprintf("%s/attachment-%08d.bin", ResourceGenerationPrefix(kind, resourceID, generationID), index)
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
