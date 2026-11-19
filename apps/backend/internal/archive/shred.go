package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type ShredStatus struct {
	Status      string     `json:"status"`
	Blocker     string     `json:"blocker,omitempty"`
	RequestedAt *time.Time `json:"requestedAt,omitempty"`
	VerifiedAt  *time.Time `json:"verifiedAt,omitempty"`
	LastError   *string    `json:"lastError,omitempty"`
}

func (s *Service) ShredEligibility(ctx context.Context, actor int64, archiveID, documentID string) (ShredStatus, error) {
	return s.shred(ctx, actor, archiveID, documentID, "", false, nil)
}

func (s *Service) AuthorizeShred(ctx context.Context, actor int64, archiveID, documentID, name string) (ShredStatus, error) {
	return s.shred(ctx, actor, archiveID, documentID, name, true, nil)
}

func (s *Service) shred(ctx context.Context, actor int64, archiveID, documentID, name string, authorize bool, target *TrashShredTarget) (ShredStatus, error) {
	out := ShredStatus{Status: "blocked"}
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) {
		return out, ErrNotFound
	}
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if _, _, err = s.membership(ctx, tx, archiveID, actor, "shred"); err != nil {
		return out, err
	}
	// Row lock serializes permission revocation with the final authorization.
	var role string
	err = tx.QueryRowContext(ctx, `SELECT role FROM invoyis.archive_memberships WHERE archive_id=$1 AND account_id=$2 FOR SHARE`, archiveID, actor).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if !Can(role, false, "shred") {
		return out, ErrForbidden
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('invoyis.include_shredded','on',true)`); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('invoyis.include_trash','on',true)`); err != nil {
		return out, err
	}
	// Lock in archive/document order before reading either the document or its
	// ledger. Concurrent requests see the first authorization after it commits.
	var lockedArchive string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM invoyis.archives WHERE id=$1 FOR SHARE`, archiveID).Scan(&lockedArchive); err != nil {
		return out, err
	}
	var kind, actual, clearance, docState string
	var revisionValue sql.NullString
	var trashed sql.NullTime
	var hasReceipt, hasOriginal bool
	docErr := tx.QueryRowContext(ctx, `SELECT a.kind,s.original_filename,d.current_confirmed_revision_id,d.clearance,d.state,d.trashed_at,
		EXISTS(SELECT 1 FROM invoyis.upload_receipts r WHERE r.archive_id=d.archive_id AND r.document_id=d.id),
		EXISTS(SELECT 1 FROM invoyis.stored_objects o WHERE o.archive_id=d.archive_id AND o.id=s.stored_object_id)
		FROM invoyis.documents d JOIN invoyis.archives a ON a.id=d.archive_id
		JOIN invoyis.submissions s ON s.id=d.submission_id WHERE d.archive_id=$1 AND d.id=$2 FOR UPDATE OF d`, archiveID, documentID).
		Scan(&kind, &actual, &revisionValue, &clearance, &docState, &trashed, &hasReceipt, &hasOriginal)
	if docErr != nil && !errors.Is(docErr, sql.ErrNoRows) {
		return out, docErr
	}
	var requested, verified sql.NullTime
	var state, failure sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT status,requested_at,verified_at,last_error FROM invoyis.document_shreds WHERE archive_id=$1 AND document_id=$2`, archiveID, documentID).Scan(&state, &requested, &verified, &failure)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		out.Status = state.String
		if requested.Valid {
			out.RequestedAt = &requested.Time
		}
		if verified.Valid {
			out.VerifiedAt = &verified.Time
		}
		if failure.Valid {
			out.LastError = &failure.String
		}
		return out, tx.Commit()
	}
	if errors.Is(docErr, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err = lockRetentionInputs(ctx, tx, archiveID, documentID); err != nil {
		return out, err
	}
	revision := revisionValue.String
	if (authorize && name != actual) || (target != nil &&
		(!trashed.Valid || !trashed.Time.Equal(target.TrashedAt) || revision != target.Revision || target.Name != actual)) {
		out.Blocker = "document_changed"
		return out, tx.Commit()
	}
	if docState != "ready" || clearance != "cleared" || revision == "" {
		out.Blocker = "cleared_confirmed_document_required"
		return out, tx.Commit()
	}
	var holds int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM invoyis.document_holds WHERE archive_id=$1 AND document_id=$2 AND released_at IS NULL`, archiveID, documentID).Scan(&holds); err != nil {
		return out, err
	}
	if holds > 0 {
		out.Blocker = "active_hold"
		return out, tx.Commit()
	}
	var busy bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM invoyis.processing_jobs WHERE archive_id=$1 AND document_id=$2 AND state IN ('pending','running')) OR EXISTS(SELECT 1 FROM invoyis.preview_claims WHERE archive_id=$1 AND document_id=$2 AND expires_at>now())`, archiveID, documentID).Scan(&busy); err != nil {
		return out, err
	}
	if busy {
		out.Blocker = "running_job"
		return out, tx.Commit()
	}
	if err = tx.QueryRowContext(ctx, `SELECT invoyis.shred_export_blocked($1,$2)`, archiveID, documentID).Scan(&busy); err != nil {
		return out, err
	}
	if busy {
		out.Blocker = "active_export"
		return out, tx.Commit()
	}
	var decision any
	fingerprint := ""
	if kind == "business" {
		candidate, e := s.readRetentionCandidate(ctx, tx, archiveID, documentID)
		if e != nil {
			return out, e
		}
		if !candidate.eligible || candidate.revision != revision {
			out.Blocker = "cleared_confirmed_document_required"
			return out, tx.Commit()
		}
		fingerprint = candidate.fingerprint
		var id, rev, storedFingerprint string
		var raw []byte
		e = tx.QueryRowContext(ctx, `SELECT id,confirmed_revision_id,fingerprint,outcome FROM invoyis.retention_evaluations WHERE archive_id=$1 AND document_id=$2 ORDER BY created_at DESC,id DESC LIMIT 1`, archiveID, documentID).Scan(&id, &rev, &storedFingerprint, &raw)
		if errors.Is(e, sql.ErrNoRows) {
			out.Blocker = "retention_missing"
			return out, tx.Commit()
		}
		if e != nil {
			return out, e
		}
		if rev != revision || storedFingerprint != fingerprint {
			out.Blocker = "retention_stale"
			return out, tx.Commit()
		}
		var outcome struct {
			Status      string `json:"status"`
			KeepThrough string `json:"keepThrough"`
		}
		if e = json.Unmarshal(raw, &outcome); e != nil || outcome.Status != "calculated" || outcome.KeepThrough == "" {
			out.Blocker = "retention_unresolved"
			return out, tx.Commit()
		}
		keep, e := time.Parse("2006-01-02", outcome.KeepThrough)
		if e != nil {
			out.Blocker = "retention_unresolved"
			return out, tx.Commit()
		}
		today, _ := time.Parse("2006-01-02", s.Now().UTC().Format("2006-01-02"))
		if !today.After(keep) {
			out.Blocker = "keep_through_date"
			return out, tx.Commit()
		}
		decision = id
	} else if kind != "personal" {
		out.Blocker = "archive_kind"
		return out, tx.Commit()
	}
	if !hasReceipt {
		out.Blocker = "receipt_missing"
		return out, tx.Commit()
	}
	if !hasOriginal {
		out.Blocker = "original_missing"
		return out, tx.Commit()
	}
	if !authorize {
		out.Status = "eligible"
		return out, tx.Commit()
	}
	var result string
	if err = tx.QueryRowContext(ctx, `SELECT invoyis.authorize_document_shred($1,$2,$3,$4,$5,$6)`, archiveID, documentID, name, revision, decision, fingerprint).Scan(&result); err != nil {
		return out, trashSQLError(err)
	}
	if result != "pending" {
		out.Blocker = result
		return out, tx.Commit()
	}
	if err = tx.QueryRowContext(ctx, `SELECT status,requested_at FROM invoyis.document_shreds WHERE archive_id=$1 AND document_id=$2`, archiveID, documentID).Scan(&out.Status, &requested); err != nil {
		return out, err
	}
	out.RequestedAt = &requested.Time
	return out, tx.Commit()
}

func (s *Service) shredEligibilityHTTP(w http.ResponseWriter, r *http.Request, id, _ string) {
	actor, _, ok := s.account(w, r, id)
	if !ok {
		return
	}
	v, e := s.ShredEligibility(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"))
	if e != nil {
		writeFailure(w, e, id)
		return
	}
	writeJSON(w, 200, id, v)
}
func (s *Service) authorizeShredHTTP(w http.ResponseWriter, r *http.Request, id, _ string) {
	actor, _, ok := s.account(w, r, id)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if e := decodeJSON(w, r, &body); e != nil {
		writeFailure(w, e, id)
		return
	}
	v, e := s.AuthorizeShred(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"), body.Name)
	if e != nil {
		writeFailure(w, e, id)
		return
	}
	writeJSON(w, 200, id, v)
}

// ReconcileOneShred can safely retry after a crash between object deletion and
// database acknowledgement. The claim and finish functions derive the exact key
// from the ledger; callers cannot supply another document's storage key.
func (s *Service) ReconcileOneShred(ctx context.Context) (bool, error) {
	store, ok := s.Storage.(DisposalStorage)
	if !ok {
		return false, ErrIntegrity
	}
	var ledgerID, archiveID, documentID, key, token string
	err := s.DB.QueryRowContext(ctx, `SELECT ledger_id,archive_id,document_id,storage_key,token FROM invoyis.claim_shred_object()`).Scan(&ledgerID, &archiveID, &documentID, &key, &token)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// The immutable ledger is constrained to one document and one original. Verify
	// that no other live reference appeared before touching object storage.
	var safe bool
	err = s.DB.QueryRowContext(ctx, `SELECT invoyis.shred_object_safe($1,$2)`, ledgerID, token).Scan(&safe)
	if err != nil {
		return true, err
	}
	var failure string
	if !safe {
		failure = "original_has_live_reference"
	} else {
		// A storage call can hang without an error. Keep it shorter than the
		// two-minute database lease so a failed key cannot stall the worker.
		storageCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		if err = store.DeleteOriginal(storageCtx, key); err != nil {
			failure = "storage_delete_failed"
		} else {
			var exists bool
			exists, err = store.OriginalExists(storageCtx, key)
			if err != nil {
				failure = "storage_verify_failed"
			} else if exists {
				failure = "object_still_present"
			}
		}
		cancel()
	}
	_, finishErr := s.DB.ExecContext(ctx, `SELECT invoyis.finish_shred_object($1,$2,$3,$4)`, ledgerID, token, failure == "", failure)
	if finishErr != nil {
		return true, finishErr
	}
	if failure != "" {
		return true, fmt.Errorf("shred %s: %s", documentID, failure)
	}
	return true, nil
}

func (s *Service) RunShredReconciler(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for i := 0; i < 20; i++ {
				found, _ := s.ReconcileOneShred(ctx)
				if !found {
					break
				}
			}
		}
	}
}
