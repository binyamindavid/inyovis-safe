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
}
type DocumentStatuses struct {
	Items []DocumentStatus `json:"items"`
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
	err = tx.QueryRowContext(ctx, `SELECT d.id,j.state,d.state,d.clearance,j.failure_code,s.original_filename
 FROM invoyis.documents d JOIN invoyis.processing_jobs j ON j.document_id=d.id
 JOIN invoyis.submissions s ON s.id=d.submission_id
 WHERE d.id=$1 AND d.archive_id=$2`, documentID, archiveID).Scan(&item.ID, &job, &document, &clearance, &failure, &item.OriginalFilename)
	if errors.Is(err, sql.ErrNoRows) {
		return DocumentStatus{}, ErrNotFound
	}
	if err != nil {
		return DocumentStatus{}, err
	}
	item.Status = documentStatus(job, document, clearance, failure.String)
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
	rows, err := tx.QueryContext(ctx, `SELECT d.id,j.state,d.state,d.clearance,j.failure_code,s.original_filename
 FROM invoyis.documents d JOIN invoyis.processing_jobs j ON j.document_id=d.id
 JOIN invoyis.submissions s ON s.id=d.submission_id WHERE d.archive_id=$1
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
		if err = rows.Scan(&item.ID, &job, &document, &clearance, &failure, &item.OriginalFilename); err != nil {
			return DocumentStatuses{}, err
		}
		item.Status = documentStatus(job, document, clearance, failure.String)
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
