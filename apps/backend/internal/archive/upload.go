package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"regexp"
	"strings"
	"time"
)

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var ErrStorageUnavailable = errors.New("upload storage unavailable")
var ErrIntegrity = errors.New("original object integrity failure")

type UploadClaim struct {
	IdempotencyKey string `json:"idempotencyKey"`
	Filename       string `json:"filename"`
	MediaType      string `json:"mediaType"`
	ByteCount      int64  `json:"byteCount"`
	SHA256         string `json:"sha256"`
}
type UploadSession struct {
	ID         string      `json:"id"`
	State      string      `json:"state"`
	FinaliseBy string      `json:"finaliseBy"`
	Grant      UploadGrant `json:"grant"`
}
type UploadReceipt struct {
	ID           string `json:"id"`
	SubmissionID string `json:"submissionId"`
	DocumentID   string `json:"documentId"`
	State        string `json:"state"`
	ByteCount    int64  `json:"byteCount"`
	SHA256       string `json:"sha256"`
	ReceivedAt   string `json:"receivedAt"`
}
type uploadRow struct {
	id, archiveID, state, filename, stagingKey, preparedKey, finalKey, declaredSHA, verifiedSHA string
	uploaderID, declaredBytes, verifiedBytes                                                    int64
	grantExpiry, finaliseBy                                                                     time.Time
}

func (s *Service) uploadLimit() int64 {
	if s.UploadCap > 0 {
		return s.UploadCap
	}
	return 25 << 20
}
func (s *Service) CreateUpload(ctx context.Context, accountID int64, archiveID string, c UploadClaim) (UploadSession, error) {
	if !uuidPattern.MatchString(archiveID) || len(c.IdempotencyKey) < 1 || len(c.IdempotencyKey) > 128 || len(c.Filename) < 1 || len(c.Filename) > 255 || strings.ContainsAny(c.Filename, "\x00/\\") || c.MediaType != "application/pdf" || c.ByteCount < 1 || c.ByteCount > s.uploadLimit() || !digestPattern.MatchString(c.SHA256) {
		return UploadSession{}, ErrInvalid
	}
	if s.Storage == nil {
		return UploadSession{}, ErrStorageUnavailable
	}
	tx, err := s.transaction(ctx, accountID, archiveID)
	if err != nil {
		return UploadSession{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, archiveID); err != nil {
		return UploadSession{}, err
	}
	if _, _, err = s.membership(ctx, tx, archiveID, accountID, "submit"); err != nil {
		return UploadSession{}, err
	}
	id, err := newID()
	if err != nil {
		return UploadSession{}, err
	}
	now := s.Now().UTC()
	grantExpiry := now.Add(15 * time.Minute)
	finaliseBy := now.Add(24 * time.Hour)
	stagingKey := "staging/" + archiveID + "/" + id
	preparedKey := "prepared/" + archiveID + "/" + id
	finalKey := "originals/" + archiveID + "/" + id
	_, err = tx.ExecContext(ctx, `INSERT INTO invoyis.upload_sessions(id,archive_id,uploader_id,idempotency_key,filename,media_type,declared_bytes,declared_sha256,staging_key,prepared_key,final_key,grant_expires_at,finalise_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(archive_id,uploader_id,idempotency_key) DO NOTHING`, id, archiveID, accountID, c.IdempotencyKey, c.Filename, c.MediaType, c.ByteCount, c.SHA256, stagingKey, preparedKey, finalKey, grantExpiry, finaliseBy)
	if err != nil {
		return UploadSession{}, err
	}
	var row uploadRow
	var filename, mediaType, claimSHA string
	var claimBytes int64
	err = tx.QueryRowContext(ctx, `SELECT id,state,staging_key,grant_expires_at,finalise_by,filename,media_type,declared_bytes,declared_sha256 FROM invoyis.upload_sessions WHERE archive_id=$1 AND uploader_id=$2 AND idempotency_key=$3 FOR UPDATE`, archiveID, accountID, c.IdempotencyKey).Scan(&row.id, &row.state, &row.stagingKey, &row.grantExpiry, &row.finaliseBy, &filename, &mediaType, &claimBytes, &claimSHA)
	if err != nil {
		return UploadSession{}, err
	}
	if filename != c.Filename || mediaType != c.MediaType || claimBytes != c.ByteCount || claimSHA != c.SHA256 {
		return UploadSession{}, ErrConflict
	}
	if row.state == "created" || row.state == "staged" {
		if now.Before(row.finaliseBy) && !now.Before(row.grantExpiry) {
			row.grantExpiry = now.Add(15 * time.Minute)
			if row.grantExpiry.After(row.finaliseBy) {
				row.grantExpiry = row.finaliseBy
			}
			if _, err = tx.ExecContext(ctx, `UPDATE invoyis.upload_sessions SET grant_expires_at=$2 WHERE id=$1`, row.id, row.grantExpiry); err != nil {
				return UploadSession{}, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return UploadSession{}, err
	}
	if row.state == "complete" || row.state == "failed" || row.state == "expired" || row.state == "finalising" || !s.Now().Before(row.finaliseBy) || !s.Now().Before(row.grantExpiry) {
		return UploadSession{}, ErrConflict
	}
	grant, err := s.Storage.Grant(ctx, row.stagingKey, row.grantExpiry, s.uploadLimit())
	if err != nil {
		return UploadSession{}, err
	}
	return UploadSession{ID: row.id, State: row.state, FinaliseBy: row.finaliseBy.UTC().Format(time.RFC3339), Grant: grant}, nil
}
func (s *Service) loadUpload(ctx context.Context, tx *sql.Tx, archiveID, sessionID string, lock bool) (uploadRow, error) {
	var row uploadRow
	var verifiedBytes sql.NullInt64
	var verifiedSHA sql.NullString
	query := `SELECT id,archive_id,uploader_id,state,filename,staging_key,prepared_key,final_key,declared_bytes,declared_sha256,verified_bytes,verified_sha256,grant_expires_at,finalise_by FROM invoyis.upload_sessions WHERE archive_id=$1 AND id=$2`
	if lock {
		query += " FOR UPDATE"
	}
	err := tx.QueryRowContext(ctx, query, archiveID, sessionID).Scan(&row.id, &row.archiveID, &row.uploaderID, &row.state, &row.filename, &row.stagingKey, &row.preparedKey, &row.finalKey, &row.declaredBytes, &row.declaredSHA, &verifiedBytes, &verifiedSHA, &row.grantExpiry, &row.finaliseBy)
	if errors.Is(err, sql.ErrNoRows) {
		return row, ErrNotFound
	}
	if err != nil {
		return row, err
	}
	row.verifiedBytes = verifiedBytes.Int64
	row.verifiedSHA = verifiedSHA.String
	return row, nil
}
func (s *Service) receipt(ctx context.Context, tx *sql.Tx, archiveID, sessionID string) (UploadReceipt, error) {
	var r UploadReceipt
	var received time.Time
	err := tx.QueryRowContext(ctx, `SELECT id,submission_id,document_id,byte_count,sha256,received_at FROM invoyis.upload_receipts WHERE archive_id=$1 AND upload_session_id=$2`, archiveID, sessionID).Scan(&r.ID, &r.SubmissionID, &r.DocumentID, &r.ByteCount, &r.SHA256, &received)
	r.State = "received"
	r.ReceivedAt = received.UTC().Format(time.RFC3339Nano)
	return r, err
}
func validateStaged(data []byte, row uploadRow, cap int64) error {
	if len(data) == 0 || int64(len(data)) > cap || int64(len(data)) != row.declaredBytes || len(data) < 5 || string(data[:5]) != "%PDF-" {
		return ErrInvalid
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != row.declaredSHA {
		return ErrInvalid
	}
	return nil
}
func (s *Service) CompleteUpload(ctx context.Context, actorID int64, archiveID, sessionID string, systemActor bool) (UploadReceipt, error) {
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(sessionID) {
		return UploadReceipt{}, ErrNotFound
	}
	if s.Storage == nil {
		return UploadReceipt{}, ErrStorageUnavailable
	}
	// For reconciliation, discover only the uploader ID, then operate with the
	// same RLS identity. The API path is always bound to its session account.
	if systemActor {
		var uploader sql.NullInt64
		err := s.DB.QueryRowContext(ctx, `SELECT invoyis.upload_uploader($1,$2)`, archiveID, sessionID).Scan(&uploader)
		if err != nil {
			return UploadReceipt{}, err
		}
		if !uploader.Valid {
			return UploadReceipt{}, ErrNotFound
		}
		actorID = uploader.Int64
	}
	tx, err := s.transaction(ctx, actorID, archiveID)
	if err != nil {
		return UploadReceipt{}, err
	}
	defer tx.Rollback()
	row, err := s.loadUpload(ctx, tx, archiveID, sessionID, true)
	if err != nil {
		return UploadReceipt{}, err
	}
	if row.uploaderID != actorID {
		return UploadReceipt{}, ErrNotFound
	}
	if row.state == "complete" {
		r, e := s.receipt(ctx, tx, archiveID, sessionID)
		return r, e
	}
	if row.state == "failed" || row.state == "expired" {
		return UploadReceipt{}, ErrConflict
	}
	if row.state != "finalising" && !s.Now().Before(row.finaliseBy) {
		return UploadReceipt{}, ErrConflict
	}
	if _, _, err = s.membership(ctx, tx, archiveID, actorID, "submit"); err != nil {
		return UploadReceipt{}, err
	}
	if row.state == "finalising" {
		tx.Rollback()
		return s.finishFinalising(ctx, actorID, row, systemActor)
	}
	if err = tx.Commit(); err != nil {
		return UploadReceipt{}, err
	}
	data, err := s.Storage.Read(ctx, row.stagingKey, s.uploadLimit())
	if err != nil {
		return UploadReceipt{}, err
	}
	if err = validateStaged(data, row, s.uploadLimit()); err != nil {
		return UploadReceipt{}, err
	}
	// Confirmation comes from Go's bounded storage read, never from a browser hint.
	stageTx, stageErr := s.transaction(ctx, actorID, archiveID)
	if stageErr != nil {
		return UploadReceipt{}, stageErr
	}
	if _, stageErr = stageTx.ExecContext(ctx, `UPDATE invoyis.upload_sessions SET state='staged' WHERE archive_id=$1 AND id=$2 AND state='created'`, archiveID, sessionID); stageErr != nil {
		stageTx.Rollback()
		return UploadReceipt{}, stageErr
	}
	if stageErr = stageTx.Commit(); stageErr != nil {
		return UploadReceipt{}, stageErr
	}
	digest := sha256.Sum256(data)
	verified := hex.EncodeToString(digest[:])
	// Preserve an immutable, private recovery copy before recording the intent.
	// Staging grants can be replayed, so recovery must not depend on staging.
	if err = s.ensureVerifiedObject(ctx, row.preparedKey, data, verified); err != nil {
		return UploadReceipt{}, err
	}
	tx, err = s.transaction(ctx, actorID, archiveID)
	if err != nil {
		return UploadReceipt{}, err
	}
	defer tx.Rollback()
	row, err = s.loadUpload(ctx, tx, archiveID, sessionID, true)
	if err != nil {
		return UploadReceipt{}, err
	}
	if row.state == "complete" {
		return s.receipt(ctx, tx, archiveID, sessionID)
	}
	if row.state == "finalising" {
		tx.Rollback()
		return s.finishFinalising(ctx, actorID, row, systemActor)
	}
	if _, _, err = s.membership(ctx, tx, archiveID, actorID, "submit"); err != nil {
		return UploadReceipt{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE invoyis.upload_sessions SET state='finalising',verified_bytes=$3,verified_sha256=$4,finalising_at=now() WHERE id=$1 AND archive_id=$2`, sessionID, archiveID, len(data), verified)
	if err != nil {
		return UploadReceipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return UploadReceipt{}, err
	}
	if s.BeforeFinalWrite != nil {
		if err := s.BeforeFinalWrite(); err != nil {
			return UploadReceipt{}, err
		}
	}
	// data is the immutable, bounded byte slice Go verified. Never re-read a
	// replayable staging key for the final write.
	if err = s.ensureVerifiedObject(ctx, row.finalKey, data, verified); err != nil {
		return UploadReceipt{}, err
	}
	if s.AfterFinalWrite != nil {
		if err := s.AfterFinalWrite(); err != nil {
			return UploadReceipt{}, err
		}
	}
	return s.finishFinalising(ctx, actorID, row, systemActor)
}
func sameDigest(data []byte, expected string) bool {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == expected
}
func (s *Service) ensureVerifiedObject(ctx context.Context, key string, data []byte, expected string) error {
	putErr := s.Storage.PutOriginal(ctx, key, data)
	actual, readErr := s.Storage.Read(ctx, key, s.uploadLimit())
	if readErr != nil {
		if putErr != nil {
			return putErr
		}
		return readErr
	}
	if int64(len(actual)) != int64(len(data)) || !sameDigest(actual, expected) {
		return ErrIntegrity
	}
	return nil
}
func (s *Service) finishFinalising(ctx context.Context, actorID int64, row uploadRow, systemActor bool) (UploadReceipt, error) {
	// Load the intent under uploader-scoped RLS, then release the transaction
	// before any storage I/O.
	readTx, err := s.transaction(ctx, actorID, row.archiveID)
	if err != nil {
		return UploadReceipt{}, err
	}
	row, err = s.loadUpload(ctx, readTx, row.archiveID, row.id, false)
	if err != nil {
		readTx.Rollback()
		return UploadReceipt{}, err
	}
	if row.state == "complete" {
		receipt, e := s.receipt(ctx, readTx, row.archiveID, row.id)
		readTx.Rollback()
		return receipt, e
	}
	if row.state != "finalising" {
		readTx.Rollback()
		return UploadReceipt{}, ErrConflict
	}
	if err = readTx.Commit(); err != nil {
		return UploadReceipt{}, err
	}

	finalData, err := s.Storage.Read(ctx, row.finalKey, s.uploadLimit())
	if errors.Is(err, ErrObjectMissing) {
		prepared, readErr := s.Storage.Read(ctx, row.preparedKey, s.uploadLimit())
		if errors.Is(readErr, ErrObjectMissing) || errors.Is(readErr, ErrInvalid) {
			return UploadReceipt{}, ErrIntegrity
		}
		if readErr != nil {
			return UploadReceipt{}, readErr
		}
		if int64(len(prepared)) != row.verifiedBytes || !sameDigest(prepared, row.verifiedSHA) {
			return UploadReceipt{}, ErrIntegrity
		}
		if err = s.ensureVerifiedObject(ctx, row.finalKey, prepared, row.verifiedSHA); err != nil {
			return UploadReceipt{}, err
		}
		finalData, err = s.Storage.Read(ctx, row.finalKey, s.uploadLimit())
	}
	if errors.Is(err, ErrInvalid) {
		return UploadReceipt{}, ErrIntegrity
	}
	if err != nil {
		return UploadReceipt{}, err
	}
	if int64(len(finalData)) != row.verifiedBytes || !sameDigest(finalData, row.verifiedSHA) {
		return UploadReceipt{}, ErrIntegrity
	}

	tx, err := s.transaction(ctx, actorID, row.archiveID)
	if err != nil {
		return UploadReceipt{}, err
	}
	defer tx.Rollback()
	locked, err := s.loadUpload(ctx, tx, row.archiveID, row.id, true)
	if err != nil {
		return UploadReceipt{}, err
	}
	if locked.state == "complete" {
		return s.receipt(ctx, tx, row.archiveID, row.id)
	}
	if locked.state != "finalising" || locked.verifiedSHA != row.verifiedSHA || locked.verifiedBytes != row.verifiedBytes {
		return UploadReceipt{}, ErrConflict
	}
	row = locked
	// Membership changes take the same advisory lock. Only the short receipt
	// transaction holds it; storage I/O has already completed.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, row.archiveID); err != nil {
		return UploadReceipt{}, err
	}
	if _, _, err = s.membership(ctx, tx, row.archiveID, actorID, "submit"); err != nil {
		return UploadReceipt{}, err
	}
	objectID, _ := newID()
	submissionID, _ := newID()
	documentID, _ := newID()
	jobID, _ := newID()
	receiptID, _ := newID()
	auditID, _ := newID()
	statements := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO invoyis.stored_objects(id,archive_id,upload_session_id,storage_key,byte_count,sha256) VALUES($1,$2,$3,$4,$5,$6)`, []any{objectID, row.archiveID, row.id, row.finalKey, row.verifiedBytes, row.verifiedSHA}},
		{`INSERT INTO invoyis.submissions(id,archive_id,upload_session_id,stored_object_id,uploader_id,source,original_filename) VALUES($1,$2,$3,$4,$5,'browser_upload',$6)`, []any{submissionID, row.archiveID, row.id, objectID, row.uploaderID, row.filename}},
		{`INSERT INTO invoyis.documents(id,archive_id,submission_id,state) VALUES($1,$2,$3,'received')`, []any{documentID, row.archiveID, submissionID}},
		{`INSERT INTO invoyis.processing_jobs(id,archive_id,document_id,state) VALUES($1,$2,$3,'pending')`, []any{jobID, row.archiveID, documentID}},
		{`INSERT INTO invoyis.upload_receipts(id,archive_id,upload_session_id,submission_id,document_id,byte_count,sha256) VALUES($1,$2,$3,$4,$5,$6,$7)`, []any{receiptID, row.archiveID, row.id, submissionID, documentID, row.verifiedBytes, row.verifiedSHA}},
	}
	for _, st := range statements {
		if _, err = tx.ExecContext(ctx, st.q, st.args...); err != nil {
			return UploadReceipt{}, err
		}
	}
	var system any
	if systemActor {
		system = "upload_reconciler"
	}
	details, _ := json.Marshal(map[string]any{"uploadSessionId": row.id, "submissionId": submissionID, "uploaderId": row.uploaderID})
	if _, err = tx.ExecContext(ctx, `INSERT INTO invoyis.archive_audit(id,archive_id,actor_id,action,target_account_id,details,system_actor) VALUES($1,$2,$3,'upload_received',$3,$4,$5)`, auditID, row.archiveID, actorID, details, system); err != nil {
		return UploadReceipt{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE invoyis.upload_sessions SET state='complete' WHERE id=$1`, row.id); err != nil {
		return UploadReceipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return UploadReceipt{}, err
	}
	tx, err = s.transaction(ctx, actorID, row.archiveID)
	if err != nil {
		return UploadReceipt{}, err
	}
	defer tx.Rollback()
	receipt, err := s.receipt(ctx, tx, row.archiveID, row.id)
	return receipt, err
}
func (s *Service) ReconcileUploads(ctx context.Context, discovery *sql.DB) (int, error) {
	if _, err := discovery.ExecContext(ctx, `SELECT invoyis.expire_abandoned_uploads()`); err != nil {
		return 0, err
	}
	rows, err := discovery.QueryContext(ctx, `SELECT archive_id,session_id FROM invoyis.stalled_upload_ids(20)`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type candidate struct{ archive, session string }
	items := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.archive, &c.session); err != nil {
			return 0, err
		}
		items = append(items, c)
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	completed := 0
	for _, c := range items {
		if err := ctx.Err(); err != nil {
			return completed, err
		}
		if _, e := s.CompleteUpload(ctx, 0, c.archive, c.session, true); e == nil {
			completed++
		} else if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) || errors.Is(e, ErrIntegrity) {
			var flagged bool
			if flagErr := discovery.QueryRowContext(ctx, `SELECT invoyis.flag_upload_orphan($1,$2)`, c.archive, c.session).Scan(&flagged); flagErr != nil {
				return completed, flagErr
			}
			if flagged {
				log.Printf("upload orphan flagged session_id=%s", c.session)
			}
		} else {
			if _, deferErr := discovery.ExecContext(ctx, `SELECT invoyis.defer_upload_reconcile($1,$2)`, c.archive, c.session); deferErr != nil {
				return completed, deferErr
			}
			log.Printf("upload reconciliation deferred session_id=%s", c.session)
		}
	}
	return completed, nil
}
