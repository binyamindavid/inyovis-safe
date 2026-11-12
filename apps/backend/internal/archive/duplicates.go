package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type DuplicateSide struct {
	DocumentID   string         `json:"documentId"`
	RevisionID   string         `json:"revisionId"`
	Filename     string         `json:"filename"`
	SupplierID   string         `json:"supplierId"`
	SupplierName string         `json:"supplierName"`
	Metadata     ReviewMetadata `json:"metadata"`
}

type DuplicateCandidate struct {
	ID            string        `json:"id"`
	Left          DuplicateSide `json:"left"`
	Right         DuplicateSide `json:"right"`
	MatchedFields []string      `json:"matchedFields"`
	CreatedAt     string        `json:"createdAt"`
}

type DuplicateDecision struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	LeftRevisionID  string `json:"leftRevisionId"`
	RightRevisionID string `json:"rightRevisionId"`
	ResolvedBy      int64  `json:"resolvedBy"`
	ResolvedAt      string `json:"resolvedAt"`
}

type ResolveDuplicateInput struct {
	Decision        string `json:"decision"`
	LeftRevisionID  string `json:"leftRevisionId"`
	RightRevisionID string `json:"rightRevisionId"`
}

var duplicateMatchedFields = []string{"linkedSupplier", "printedNumber", "issueDate", "total", "currency"}

// The archive lock is acquired before the document lock by confirmations and
// resolutions. Two independent confirmations therefore cannot both miss a pair.
func lockDuplicateArchive(ctx context.Context, tx *sql.Tx, archiveID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),12)`, archiveID)
	return err
}

// A candidate requires two distinct originals and two current, cleared,
// confirmed invoice revisions. Unlinked suppliers and missing comparison fields
// are ineligible; printed supplier text remains visible but is never identity.
func refreshDuplicateCandidates(ctx context.Context, tx *sql.Tx, archiveID, documentID, revisionID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE invoyis.duplicate_candidates SET state='stale',updated_at=now()
 WHERE archive_id=$1 AND ((left_document_id=$2 AND left_revision_id<>$3)
 OR (right_document_id=$2 AND right_revision_id<>$3)) AND state<>'stale'`, archiveID, documentID, revisionID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO invoyis.duplicate_candidates
 (id,archive_id,left_document_id,right_document_id,left_revision_id,right_revision_id)
 SELECT gen_random_uuid(),$1,
  least(d.id,other.id),greatest(d.id,other.id),
  CASE WHEN d.id<other.id THEN r.id ELSE r2.id END,
  CASE WHEN d.id<other.id THEN r2.id ELSE r.id END
 FROM invoyis.documents d
 JOIN invoyis.document_revisions r ON r.id=d.current_confirmed_revision_id AND r.archive_id=d.archive_id
 JOIN invoyis.submissions sub ON sub.id=d.submission_id AND sub.archive_id=d.archive_id
 JOIN invoyis.stored_objects o ON o.id=sub.stored_object_id AND o.archive_id=d.archive_id
 JOIN invoyis.documents other ON other.archive_id=d.archive_id AND other.id<>d.id AND other.state='ready' AND other.clearance='cleared'
 JOIN invoyis.document_revisions r2 ON r2.id=other.current_confirmed_revision_id AND r2.archive_id=other.archive_id
 JOIN invoyis.submissions sub2 ON sub2.id=other.submission_id AND sub2.archive_id=other.archive_id
 JOIN invoyis.stored_objects o2 ON o2.id=sub2.stored_object_id AND o2.archive_id=other.archive_id
 WHERE d.archive_id=$1 AND d.id=$2 AND d.current_confirmed_revision_id=$3
 AND d.state='ready' AND d.clearance='cleared' AND o.id<>o2.id AND o.sha256<>o2.sha256
 AND r.kind='confirmed' AND r2.kind='confirmed'
 AND r.supplier_id IS NOT NULL AND r.supplier_id=r2.supplier_id
 AND nullif(r.metadata->>'invoiceId','') IS NOT NULL AND r.metadata->>'invoiceId'=r2.metadata->>'invoiceId'
 AND r.issue_date IS NOT NULL AND r.issue_date=r2.issue_date
 AND r.total IS NOT NULL AND r.total=r2.total
 AND nullif(r.metadata->>'currency','') IS NOT NULL AND r.metadata->>'currency'=r2.metadata->>'currency'
 ON CONFLICT (archive_id,left_document_id,right_document_id) DO UPDATE SET
 left_revision_id=EXCLUDED.left_revision_id,right_revision_id=EXCLUDED.right_revision_id,
 state='pending',resolved_by=NULL,resolved_at=NULL,
 resolved_left_revision_id=NULL,resolved_right_revision_id=NULL,updated_at=now()
 WHERE invoyis.duplicate_candidates.left_revision_id<>EXCLUDED.left_revision_id
 OR invoyis.duplicate_candidates.right_revision_id<>EXCLUDED.right_revision_id
 OR invoyis.duplicate_candidates.state='stale'`, archiveID, documentID, revisionID)
	return err
}

func (s *Service) ListDuplicateCandidates(ctx context.Context, actor int64, archiveID string) ([]DuplicateCandidate, error) {
	if !uuidPattern.MatchString(archiveID) {
		return nil, ErrNotFound
	}
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, _, err = s.membership(ctx, tx, archiveID, actor, "view"); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.left_document_id,c.left_revision_id,ls.original_filename,lr.supplier_id,lsp.display_name,lr.metadata,
	 c.right_document_id,c.right_revision_id,rs.original_filename,rr.supplier_id,rsp.display_name,rr.metadata,c.created_at
 FROM invoyis.duplicate_candidates c
 JOIN invoyis.documents ld ON ld.id=c.left_document_id AND ld.archive_id=c.archive_id AND ld.current_confirmed_revision_id=c.left_revision_id AND ld.clearance='cleared' AND ld.state='ready'
 JOIN invoyis.documents rd ON rd.id=c.right_document_id AND rd.archive_id=c.archive_id AND rd.current_confirmed_revision_id=c.right_revision_id AND rd.clearance='cleared' AND rd.state='ready'
 JOIN invoyis.document_revisions lr ON lr.id=c.left_revision_id AND lr.archive_id=c.archive_id
 JOIN invoyis.document_revisions rr ON rr.id=c.right_revision_id AND rr.archive_id=c.archive_id
	 JOIN invoyis.suppliers lsp ON lsp.id=lr.supplier_id AND lsp.archive_id=c.archive_id
	 JOIN invoyis.suppliers rsp ON rsp.id=rr.supplier_id AND rsp.archive_id=c.archive_id
 JOIN invoyis.submissions ls ON ls.id=ld.submission_id AND ls.archive_id=c.archive_id
 JOIN invoyis.submissions rs ON rs.id=rd.submission_id AND rs.archive_id=c.archive_id
 WHERE c.archive_id=$1 AND c.state='pending' ORDER BY c.created_at,c.id LIMIT 100`, archiveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []DuplicateCandidate{}
	for rows.Next() {
		var c DuplicateCandidate
		var leftMeta, rightMeta []byte
		var created time.Time
		if err = rows.Scan(&c.ID, &c.Left.DocumentID, &c.Left.RevisionID, &c.Left.Filename, &c.Left.SupplierID, &c.Left.SupplierName, &leftMeta,
			&c.Right.DocumentID, &c.Right.RevisionID, &c.Right.Filename, &c.Right.SupplierID, &c.Right.SupplierName, &rightMeta, &created); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(leftMeta, &c.Left.Metadata); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(rightMeta, &c.Right.Metadata); err != nil {
			return nil, err
		}
		c.MatchedFields = duplicateMatchedFields
		c.CreatedAt = created.UTC().Format(time.RFC3339Nano)
		items = append(items, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, tx.Commit()
}

func (s *Service) ResolveDuplicate(ctx context.Context, actor int64, archiveID, candidateID string, input ResolveDuplicateInput) (DuplicateDecision, error) {
	var result DuplicateDecision
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(candidateID) {
		return result, ErrNotFound
	}
	if input.Decision != "duplicate" && input.Decision != "keep_both" || !uuidPattern.MatchString(input.LeftRevisionID) || !uuidPattern.MatchString(input.RightRevisionID) {
		return result, ErrInvalid
	}
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if _, _, err = s.membership(ctx, tx, archiveID, actor, "correct"); err != nil {
		return result, err
	}
	if err = lockDuplicateArchive(ctx, tx, archiveID); err != nil {
		return result, err
	}
	var leftDoc, rightDoc, leftRev, rightRev, state string
	var resolvedBy sql.NullInt64
	var resolvedAt sql.NullTime
	var resolvedLeft, resolvedRight sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT left_document_id,right_document_id,left_revision_id,right_revision_id,state,resolved_by,resolved_at,resolved_left_revision_id,resolved_right_revision_id
 FROM invoyis.duplicate_candidates WHERE archive_id=$1 AND id=$2 FOR UPDATE`, archiveID, candidateID).
		Scan(&leftDoc, &rightDoc, &leftRev, &rightRev, &state, &resolvedBy, &resolvedAt, &resolvedLeft, &resolvedRight)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if state == input.Decision && resolvedBy.Valid && resolvedBy.Int64 == actor && resolvedLeft.String == input.LeftRevisionID && resolvedRight.String == input.RightRevisionID {
		return DuplicateDecision{ID: candidateID, State: state, LeftRevisionID: resolvedLeft.String, RightRevisionID: resolvedRight.String, ResolvedBy: actor, ResolvedAt: resolvedAt.Time.UTC().Format(time.RFC3339Nano)}, tx.Commit()
	}
	if state != "pending" || leftRev != input.LeftRevisionID || rightRev != input.RightRevisionID {
		return result, ErrConflict
	}
	var current bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM invoyis.documents l JOIN invoyis.documents r ON r.archive_id=l.archive_id
 WHERE l.archive_id=$1 AND l.id=$2 AND r.id=$3 AND l.state='ready' AND r.state='ready'
 AND l.clearance='cleared' AND r.clearance='cleared'
 AND l.current_confirmed_revision_id=$4 AND r.current_confirmed_revision_id=$5)`, archiveID, leftDoc, rightDoc, leftRev, rightRev).Scan(&current)
	if err != nil {
		return result, err
	}
	if !current {
		return result, ErrConflict
	}
	var when time.Time
	err = tx.QueryRowContext(ctx, `UPDATE invoyis.duplicate_candidates SET state=$3,resolved_by=$4,resolved_at=now(),
 resolved_left_revision_id=left_revision_id,resolved_right_revision_id=right_revision_id,updated_at=now()
 WHERE archive_id=$1 AND id=$2 RETURNING resolved_at`, archiveID, candidateID, input.Decision, actor).Scan(&when)
	if err != nil {
		return result, err
	}
	if err = s.audit(ctx, tx, archiveID, actor, "duplicate_candidate_resolved", nil, map[string]any{"candidateId": candidateID, "decision": input.Decision, "leftDocumentId": leftDoc, "rightDocumentId": rightDoc, "leftRevisionId": leftRev, "rightRevisionId": rightRev}); err != nil {
		return result, err
	}
	result = DuplicateDecision{ID: candidateID, State: input.Decision, LeftRevisionID: leftRev, RightRevisionID: rightRev, ResolvedBy: actor, ResolvedAt: when.UTC().Format(time.RFC3339Nano)}
	return result, tx.Commit()
}

func (s *Service) listDuplicateCandidatesHTTP(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	items, err := s.ListDuplicateCandidates(r.Context(), actor, r.PathValue("archiveId"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 200, requestID, map[string]any{"items": items})
}

func (s *Service) resolveDuplicateHTTP(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var input ResolveDuplicateInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeFailure(w, err, requestID)
		return
	}
	result, err := s.ResolveDuplicate(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"), input)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 200, requestID, result)
}
