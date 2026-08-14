package state

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

var (
	ErrImageUploadNotFound     = errors.New("image upload not found")
	ErrImageUploadChanged      = errors.New("image upload changed")
	ErrImageUploadRangeOverlap = errors.New("image upload range overlap")
	ErrImageRevisionNotFound   = errors.New("image revision not found")
)

type ImageUploadIdentity struct {
	Repository    string `json:"repository"`
	Ref           string `json:"ref"`
	SHA           string `json:"sha"`
	CommitMessage string `json:"commitMessage,omitempty"`
	Workflow      string `json:"workflow"`
	WorkflowRef   string `json:"workflowRef"`
	Actor         string `json:"actor"`
	RunID         string `json:"runId"`
	RunAttempt    string `json:"runAttempt"`
}

type ImageUploadByteRange struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

type ImageUpload struct {
	ID              string
	ServiceID       string
	Tag             string
	ExpectedLength  int64
	ReceivedLength  int64
	ReceivedRanges  []ImageUploadByteRange
	ExpectedSHA256  string
	TemporaryPath   string
	Identity        ImageUploadIdentity
	Status          string
	ImageRevisionID string
	DeploymentID    string
	PreviewID       string
	PreviewURL      string
	ImageDigest     string
	ErrorCode       string
	ErrorMessage    string
	CreatedAtMillis int64
	UpdatedAtMillis int64
	ExpiresAtMillis int64
}

type BeginImageUploadInput struct {
	ID              string
	ServiceID       string
	Tag             string
	ExpectedLength  int64
	ExpectedSHA256  string
	TemporaryPath   string
	Identity        ImageUploadIdentity
	CreatedAtMillis int64
	ExpiresAtMillis int64
}

func (store *Store) BeginImageUpload(ctx context.Context, input BeginImageUploadInput) ([]string, error) {
	if input.ID == "" || input.ServiceID == "" || input.Tag == "" || input.ExpectedLength <= 0 ||
		len(input.ExpectedSHA256) != 64 || input.TemporaryPath == "" || input.CreatedAtMillis <= 0 ||
		input.ExpiresAtMillis <= input.CreatedAtMillis {
		return nil, errors.New("begin image upload input is incomplete")
	}
	identityJSON, err := json.Marshal(input.Identity)
	if err != nil {
		return nil, err
	}
	var supersededPaths []string
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var processing int
		if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM service_image_uploads
  WHERE service_id = ? AND tag = ? AND status IN ('importing', 'deploying')
)`, input.ServiceID, input.Tag).Scan(&processing); err != nil {
			return err
		}
		if processing != 0 {
			return ErrImageUploadChanged
		}
		rows, err := transaction.QueryContext(ctx, `
SELECT temporary_path FROM service_image_uploads
WHERE service_id = ? AND tag = ? AND status = 'uploading'`, input.ServiceID, input.Tag)
		if err != nil {
			return err
		}
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				_ = rows.Close()
				return err
			}
			supersededPaths = append(supersededPaths, path)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE service_image_uploads
SET status = 'superseded', error_code = 'superseded', error_message = 'A newer upload replaced this upload',
    updated_at = ?, expires_at = ?
WHERE service_id = ? AND tag = ? AND status = 'uploading'`,
			input.CreatedAtMillis, input.ExpiresAtMillis, input.ServiceID, input.Tag,
		); err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO service_image_uploads(
  id, service_id, tag, expected_length, expected_sha256, temporary_path,
  oidc_metadata_json, status, created_at, updated_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'uploading', ?, ?, ?)`,
			input.ID, input.ServiceID, input.Tag, input.ExpectedLength, input.ExpectedSHA256,
			input.TemporaryPath, string(identityJSON), input.CreatedAtMillis, input.CreatedAtMillis, input.ExpiresAtMillis,
		)
		return err
	})
	return supersededPaths, err
}

func (store *Store) ImageUpload(ctx context.Context, uploadID, serviceID string) (ImageUpload, error) {
	upload, err := scanImageUpload(store.database.QueryRowContext(ctx, imageUploadSelect+` WHERE id = ? AND service_id = ?`, uploadID, serviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return ImageUpload{}, ErrImageUploadNotFound
	}
	return upload, err
}

// RecordImageUploadPart records a non-overlapping byte range. Exact duplicate
// ranges are idempotent. Returns the updated upload; Complete is true when the
// union of ranges covers [0, ExpectedLength).
func (store *Store) RecordImageUploadPart(
	ctx context.Context,
	uploadID string,
	offset, length, updatedAtMillis int64,
) (upload ImageUpload, complete bool, err error) {
	if uploadID == "" || offset < 0 || length <= 0 || updatedAtMillis <= 0 {
		return ImageUpload{}, false, errors.New("record image upload part input is incomplete")
	}
	err = store.Write(ctx, func(transaction *sql.Tx) error {
		current, scanErr := scanImageUpload(transaction.QueryRowContext(ctx, imageUploadSelect+` WHERE id = ?`, uploadID))
		if errors.Is(scanErr, sql.ErrNoRows) {
			return ErrImageUploadNotFound
		}
		if scanErr != nil {
			return scanErr
		}
		if current.Status != "uploading" {
			return ErrImageUploadChanged
		}
		if offset+length < offset || offset+length > current.ExpectedLength {
			return errors.New("upload part exceeds expected length")
		}
		if existing := findExactRange(current.ReceivedRanges, offset, length); existing {
			upload = current
			complete = ImageUploadCoverageComplete(current.ReceivedRanges, current.ExpectedLength)
			return nil
		}
		if rangesOverlap(current.ReceivedRanges, offset, length) {
			return ErrImageUploadRangeOverlap
		}
		ranges := append(append([]ImageUploadByteRange{}, current.ReceivedRanges...), ImageUploadByteRange{
			Offset: offset, Length: length,
		})
		encoded, marshalErr := json.Marshal(ranges)
		if marshalErr != nil {
			return marshalErr
		}
		received := current.ReceivedLength + length
		result, execErr := transaction.ExecContext(ctx, `
UPDATE service_image_uploads
SET received_length = ?, received_ranges_json = ?, updated_at = ?
WHERE id = ? AND status = 'uploading' AND received_length = ?`,
			received, string(encoded), updatedAtMillis, uploadID, current.ReceivedLength,
		)
		if execErr != nil {
			return execErr
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil || changed != 1 {
			return ErrImageUploadChanged
		}
		current.ReceivedLength = received
		current.ReceivedRanges = ranges
		current.UpdatedAtMillis = updatedAtMillis
		upload = current
		complete = ImageUploadCoverageComplete(ranges, current.ExpectedLength)
		return nil
	})
	return upload, complete, err
}

func findExactRange(ranges []ImageUploadByteRange, offset, length int64) bool {
	for _, item := range ranges {
		if item.Offset == offset && item.Length == length {
			return true
		}
	}
	return false
}

func rangesOverlap(ranges []ImageUploadByteRange, offset, length int64) bool {
	end := offset + length
	for _, item := range ranges {
		itemEnd := item.Offset + item.Length
		if offset < itemEnd && end > item.Offset {
			return true
		}
	}
	return false
}

// ImageUploadCoverageComplete reports whether ranges form a gap-free cover of
// [0, expectedLength).
func ImageUploadCoverageComplete(ranges []ImageUploadByteRange, expectedLength int64) bool {
	if expectedLength <= 0 {
		return false
	}
	ordered := append([]ImageUploadByteRange{}, ranges...)
	slices.SortFunc(ordered, func(left, right ImageUploadByteRange) int {
		return cmp.Compare(left.Offset, right.Offset)
	})
	var cursor int64
	for _, item := range ordered {
		if item.Offset != cursor || item.Length <= 0 {
			return false
		}
		cursor += item.Length
	}
	return cursor == expectedLength
}

func (store *Store) SetImageUploadStatus(ctx context.Context, uploadID, from, to string, updatedAtMillis int64) error {
	if uploadID == "" || from == "" || to == "" || updatedAtMillis <= 0 {
		return errors.New("image upload status input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `UPDATE service_image_uploads SET status = ?, updated_at = ? WHERE id = ? AND status = ?`, to, updatedAtMillis, uploadID, from)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrImageUploadChanged
		}
		return nil
	})
}

type ImageRevision struct {
	ID                string
	ServiceID         string
	Tag               string
	Kind              string
	ArchivePath       string
	ArchiveSHA256     string
	ImageDigest       string
	DeploymentID      string
	PreviewID         string
	Identity          ImageUploadIdentity
	Status            string
	CreatedAtMillis   int64
	ActivatedAtMillis int64
	RetiredAtMillis   int64
	ExpiresAtMillis   int64
}

type CreateImageRevisionInput struct {
	ID              string
	UploadID        string
	ServiceID       string
	Tag             string
	Kind            string
	ArchivePath     string
	ArchiveSHA256   string
	Identity        ImageUploadIdentity
	CreatedAtMillis int64
	ExpiresAtMillis int64
}

func (store *Store) CreateImageRevision(ctx context.Context, input CreateImageRevisionInput) error {
	if input.ID == "" || input.UploadID == "" || input.ServiceID == "" || input.Tag == "" ||
		(input.Kind != "production" && input.Kind != "preview") || input.ArchivePath == "" ||
		len(input.ArchiveSHA256) != 64 || input.CreatedAtMillis <= 0 {
		return errors.New("create image revision input is incomplete")
	}
	identityJSON, err := json.Marshal(input.Identity)
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var expires any
		if input.ExpiresAtMillis > 0 {
			expires = input.ExpiresAtMillis
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO service_image_revisions(
 id, service_id, tag, kind, archive_path, archive_sha256, oidc_metadata_json,
 status, created_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'importing', ?, ?)`,
			input.ID, input.ServiceID, input.Tag, input.Kind, input.ArchivePath,
			input.ArchiveSHA256, string(identityJSON), input.CreatedAtMillis, expires,
		); err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE service_image_uploads SET image_revision_id = ?, status = 'importing', updated_at = ?
WHERE id = ? AND service_id = ? AND status = 'uploading' AND received_length = expected_length`,
			input.ID, input.CreatedAtMillis, input.UploadID, input.ServiceID,
		)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrImageUploadChanged
		}
		return nil
	})
}

func (store *Store) SetImageRevisionImported(ctx context.Context, revisionID, uploadID, digest, previewID string, updatedAtMillis int64) error {
	if revisionID == "" || uploadID == "" || digest == "" || updatedAtMillis <= 0 {
		return errors.New("import image revision input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE service_image_revisions SET image_digest = ?, preview_id = ?
WHERE id = ? AND status = 'importing'`, digest, nullableString(previewID), revisionID)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrImageRevisionNotFound
		}
		result, err = transaction.ExecContext(ctx, `
UPDATE service_image_uploads SET status = 'deploying', image_digest = ?, preview_id = ?, updated_at = ?
WHERE id = ? AND image_revision_id = ? AND status = 'importing'`,
			digest, nullableString(previewID), updatedAtMillis, uploadID, revisionID,
		)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrImageUploadChanged
		}
		return nil
	})
}

func (store *Store) CompleteImageUpload(
	ctx context.Context,
	uploadID, revisionID, previewURL string,
	completedAtMillis, uploadExpiresAtMillis, previewExpiresAtMillis int64,
) error {
	if uploadID == "" || revisionID == "" || completedAtMillis <= 0 ||
		uploadExpiresAtMillis <= completedAtMillis || previewExpiresAtMillis <= completedAtMillis {
		return errors.New("complete image upload input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var serviceID, tag string
		if err := transaction.QueryRowContext(ctx, `SELECT service_id, tag FROM service_image_revisions WHERE id = ? AND status IN ('importing', 'active')`, revisionID).Scan(&serviceID, &tag); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrImageRevisionNotFound
			}
			return err
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE service_image_revisions SET status = 'retired', retired_at = ?
WHERE service_id = ? AND tag = ? AND status = 'active' AND id != ?`, completedAtMillis, serviceID, tag, revisionID); err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE service_image_revisions SET status = 'active', activated_at = COALESCE(activated_at, ?),
    expires_at = CASE WHEN kind = 'preview' THEN ? ELSE NULL END
WHERE id = ? AND status IN ('importing', 'active')`, completedAtMillis, previewExpiresAtMillis, revisionID)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrImageRevisionNotFound
		}
		result, err = transaction.ExecContext(ctx, `
UPDATE service_image_uploads SET status = 'succeeded', preview_url = ?, updated_at = ?, expires_at = ?
WHERE id = ? AND image_revision_id = ? AND status = 'deploying'`, nullableString(previewURL), completedAtMillis, uploadExpiresAtMillis, uploadID, revisionID)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrImageUploadChanged
		}
		return nil
	})
}

func (store *Store) FailImageUpload(ctx context.Context, uploadID, code, message string, failedAtMillis, expiresAtMillis int64) error {
	if uploadID == "" || code == "" || message == "" || failedAtMillis <= 0 || expiresAtMillis <= failedAtMillis {
		return errors.New("fail image upload input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		// An imported archive (digest set) remains reusable after deploy failure.
		// Mark it retired so env changes / redeploy can override without waiting for
		// another upload; only mark failed when import never completed.
		if _, err := transaction.ExecContext(ctx, `
UPDATE service_image_revisions
SET status = CASE WHEN IFNULL(image_digest, '') != '' THEN 'retired' ELSE 'failed' END,
    retired_at = ?
WHERE id = (SELECT image_revision_id FROM service_image_uploads WHERE id = ?) AND status = 'importing'`, failedAtMillis, uploadID); err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE service_image_uploads SET status = 'failed', error_code = ?, error_message = ?, updated_at = ?, expires_at = ?
WHERE id = ? AND status IN ('uploading', 'importing', 'deploying')`, code, message, failedAtMillis, expiresAtMillis, uploadID)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrImageUploadChanged
		}
		return nil
	})
}

func (store *Store) ImageRevision(ctx context.Context, revisionID string) (ImageRevision, error) {
	revision, err := scanImageRevision(store.database.QueryRowContext(ctx, imageRevisionSelect+` WHERE id = ?`, revisionID))
	if errors.Is(err, sql.ErrNoRows) {
		return ImageRevision{}, ErrImageRevisionNotFound
	}
	return revision, err
}

func (store *Store) ActiveProductionImageRevision(ctx context.Context, serviceID string) (ImageRevision, error) {
	revision, err := scanImageRevision(store.database.QueryRowContext(ctx, imageRevisionSelect+` WHERE service_id = ? AND tag = 'latest' AND status = 'active'`, serviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return ImageRevision{}, ErrImageRevisionNotFound
	}
	return revision, err
}

// LatestReusableProductionRevision returns the newest imported production archive
// even when no deployment row exists yet (deploy failed or daemon restarted after
// import). Used so env/reconcile can override without requiring another upload.
func (store *Store) LatestReusableProductionRevision(ctx context.Context, serviceID string) (ImageRevision, error) {
	if serviceID == "" {
		return ImageRevision{}, errors.New("service ID is required")
	}
	revision, err := scanImageRevision(store.database.QueryRowContext(ctx, imageRevisionSelect+`
WHERE service_id = ? AND kind = 'production'
  AND IFNULL(image_digest, '') != ''
  AND IFNULL(archive_path, '') != ''
  AND status IN ('importing', 'active', 'retired', 'failed')
ORDER BY created_at DESC, id DESC
LIMIT 1`, serviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return ImageRevision{}, ErrImageRevisionNotFound
	}
	return revision, err
}

const imageUploadSelect = `SELECT id, service_id, tag, expected_length, received_length,
received_ranges_json, expected_sha256, temporary_path, oidc_metadata_json, status, image_revision_id,
deployment_id, preview_id, preview_url, image_digest, error_code, error_message,
created_at, updated_at, expires_at FROM service_image_uploads`

type rowScanner interface{ Scan(...any) error }

func scanImageUpload(scanner rowScanner) (ImageUpload, error) {
	var upload ImageUpload
	var identityJSON, rangesJSON string
	var revisionID, deploymentID, previewID, previewURL, digest, errorCode, errorMessage sql.NullString
	if err := scanner.Scan(
		&upload.ID, &upload.ServiceID, &upload.Tag, &upload.ExpectedLength, &upload.ReceivedLength,
		&rangesJSON, &upload.ExpectedSHA256, &upload.TemporaryPath, &identityJSON, &upload.Status, &revisionID,
		&deploymentID, &previewID, &previewURL, &digest, &errorCode, &errorMessage,
		&upload.CreatedAtMillis, &upload.UpdatedAtMillis, &upload.ExpiresAtMillis,
	); err != nil {
		return ImageUpload{}, err
	}
	if err := json.Unmarshal([]byte(identityJSON), &upload.Identity); err != nil {
		return ImageUpload{}, fmt.Errorf("decode image upload identity: %w", err)
	}
	if rangesJSON == "" {
		rangesJSON = "[]"
	}
	if err := json.Unmarshal([]byte(rangesJSON), &upload.ReceivedRanges); err != nil {
		return ImageUpload{}, fmt.Errorf("decode image upload ranges: %w", err)
	}
	if upload.ReceivedRanges == nil {
		upload.ReceivedRanges = []ImageUploadByteRange{}
	}
	upload.ImageRevisionID, upload.DeploymentID, upload.PreviewID = revisionID.String, deploymentID.String, previewID.String
	upload.PreviewURL, upload.ImageDigest = previewURL.String, digest.String
	upload.ErrorCode, upload.ErrorMessage = errorCode.String, errorMessage.String
	return upload, nil
}

const imageRevisionSelect = `SELECT id, service_id, tag, kind, archive_path, archive_sha256,
image_digest, deployment_id, preview_id, oidc_metadata_json, status, created_at,
activated_at, retired_at, expires_at FROM service_image_revisions`

func scanImageRevision(scanner rowScanner) (ImageRevision, error) {
	var revision ImageRevision
	var digest, deploymentID, previewID sql.NullString
	var identityJSON string
	var activated, retired, expires sql.NullInt64
	if err := scanner.Scan(&revision.ID, &revision.ServiceID, &revision.Tag, &revision.Kind,
		&revision.ArchivePath, &revision.ArchiveSHA256, &digest, &deploymentID, &previewID,
		&identityJSON, &revision.Status, &revision.CreatedAtMillis, &activated, &retired, &expires,
	); err != nil {
		return ImageRevision{}, err
	}
	if err := json.Unmarshal([]byte(identityJSON), &revision.Identity); err != nil {
		return ImageRevision{}, fmt.Errorf("decode image revision identity: %w", err)
	}
	revision.ImageDigest, revision.DeploymentID, revision.PreviewID = digest.String, deploymentID.String, previewID.String
	revision.ActivatedAtMillis, revision.RetiredAtMillis, revision.ExpiresAtMillis = activated.Int64, retired.Int64, expires.Int64
	return revision, nil
}
