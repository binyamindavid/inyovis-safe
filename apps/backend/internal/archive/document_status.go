package archive

import (
	"context"
	"database/sql"
	"errors"
)

type DocumentStatus struct {
	ID               string `json:"id"`
	Status           string `json:"status"`
	OriginalFilename string `json:"originalFilename"`
	// ConfirmedType and ConfirmedTitle come only from the confirmed revision, never a later draft.
	ConfirmedType     *string `json:"confirmedType"`
	ConfirmedTitle    *string `json:"confirmedTitle"`
	IssueDate         *string `json:"issueDate"`
	PrintedSupplier   *string `json:"printedSupplier"`
	DocumentNumber    *string `json:"documentNumber"`
	AccountReference  *string `json:"accountReference"`
	Total             *string `json:"total"`
	Currency          *string `json:"currency"`
	SupplierID        *string `json:"supplierId"`
	SupplierName      *string `json:"supplierName"`
	CategoryID        *string `json:"categoryId"`
	CategoryName      *string `json:"categoryName"`
	PropertyID        *string `json:"propertyId"`
	PropertyName      *string `json:"propertyName"`
	PaperLocationID   *string `json:"paperLocationId"`
	PaperLocationName *string `json:"paperLocationName"`
	ReferenceScan     bool    `json:"referenceScan"`
}
type DocumentStatuses struct {
	Items      []DocumentStatus `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

// hideUnclearedMetadata keeps confirmed metadata out of responses for a document that
// is no longer cleared, matching the review read check.
func (d *DocumentStatus) hideUnclearedMetadata(clearance string) {
	if clearance != "cleared" {
		d.ConfirmedType, d.ConfirmedTitle = nil, nil
	}
}
func documentStatus(jobState, documentState, clearance, failure string) string {
	switch {
	case clearance == "blocked" && failure == "scan_rejected":
		return "file_blocked"
	case documentState == "failed" || jobState == "failed":
		return "processing_failed"
	case clearance == "cleared" && documentState == "ready":
		return "ready"
	case clearance == "cleared" && documentState == "needs_review":
		return "needs_review"
	case jobState == "running":
		return "processing"
	default:
		return "pending"
	}
}
func (s *Service) DocumentStatus(ctx context.Context, accountID int64, archiveID, documentID string) (DocumentStatus, error) {
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) {
		return DocumentStatus{}, ErrNotFound
	}
	tx, err := s.transaction(ctx, accountID, archiveID)
	if err != nil {
		return DocumentStatus{}, err
	}
	defer tx.Rollback()
	if _, _, err = s.membership(ctx, tx, archiveID, accountID, "view"); err != nil {
		if errors.Is(err, ErrForbidden) {
			return DocumentStatus{}, ErrNotFound
		}
		return DocumentStatus{}, err
	}
	var item DocumentStatus
	var job, document, clearance string
	var failure sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT d.id,j.state,d.state,d.clearance,j.failure_code,s.original_filename,
 NULLIF(r.metadata->>'documentType',''),NULLIF(r.metadata->>'title','')
 FROM invoyis.documents d JOIN invoyis.processing_jobs j ON j.document_id=d.id
 JOIN invoyis.submissions s ON s.id=d.submission_id
 LEFT JOIN invoyis.document_revisions r ON r.archive_id=d.archive_id AND r.document_id=d.id AND r.id=d.current_confirmed_revision_id
 WHERE d.id=$1 AND d.archive_id=$2`, documentID, archiveID).Scan(&item.ID, &job, &document, &clearance, &failure, &item.OriginalFilename, &item.ConfirmedType, &item.ConfirmedTitle)
	if errors.Is(err, sql.ErrNoRows) {
		return DocumentStatus{}, ErrNotFound
	}
	if err != nil {
		return DocumentStatus{}, err
	}
	item.Status = documentStatus(job, document, clearance, failure.String)
	item.hideUnclearedMetadata(clearance)
	if err = tx.Commit(); err != nil {
		return DocumentStatus{}, err
	}
	return item, nil
}
func (s *Service) ListDocumentStatuses(ctx context.Context, accountID int64, archiveID string) (DocumentStatuses, error) {
	if !uuidPattern.MatchString(archiveID) {
		return DocumentStatuses{}, ErrNotFound
	}
	tx, err := s.transaction(ctx, accountID, archiveID)
	if err != nil {
		return DocumentStatuses{}, err
	}
	defer tx.Rollback()
	if _, _, err = s.membership(ctx, tx, archiveID, accountID, "view"); err != nil {
		if errors.Is(err, ErrForbidden) {
			return DocumentStatuses{}, ErrNotFound
		}
		return DocumentStatuses{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.id,j.state,d.state,d.clearance,j.failure_code,s.original_filename,
 NULLIF(r.metadata->>'documentType',''),NULLIF(r.metadata->>'title','')
 FROM invoyis.documents d JOIN invoyis.processing_jobs j ON j.document_id=d.id
 JOIN invoyis.submissions s ON s.id=d.submission_id
 LEFT JOIN invoyis.document_revisions r ON r.archive_id=d.archive_id AND r.document_id=d.id AND r.id=d.current_confirmed_revision_id WHERE d.archive_id=$1
 ORDER BY d.created_at DESC,d.id DESC LIMIT 20`, archiveID)
	if err != nil {
		return DocumentStatuses{}, err
	}
	defer rows.Close()
	result := DocumentStatuses{Items: []DocumentStatus{}}
	for rows.Next() {
		var item DocumentStatus
		var job, document, clearance string
		var failure sql.NullString
		if err = rows.Scan(&item.ID, &job, &document, &clearance, &failure, &item.OriginalFilename, &item.ConfirmedType, &item.ConfirmedTitle); err != nil {
			return DocumentStatuses{}, err
		}
		item.Status = documentStatus(job, document, clearance, failure.String)
		item.hideUnclearedMetadata(clearance)
		result.Items = append(result.Items, item)
	}
	if err = rows.Err(); err != nil {
		return DocumentStatuses{}, err
	}
	if err = tx.Commit(); err != nil {
		return DocumentStatuses{}, err
	}
	return result, nil
}
