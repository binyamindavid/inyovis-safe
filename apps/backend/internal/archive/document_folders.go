package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// folderNone is the folder ID for documents with no confirmed linked supplier or category ("Unassigned company",
// "Uncategorised"). It is accepted by the document list's supplierId and categoryId filters.
const folderNone = "none"

// DocumentFolder is one archive folder: a supplier identity or a filing category, with the number of documents filed
// under it. Count covers every document the member can list, across all pages and statuses.
type DocumentFolder struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Archived    bool   `json:"archived"`
	Count       int64  `json:"count"`
}

// DocumentFolders groups an archive's documents by company (linked supplier) or by filing category. A document is in
// exactly one folder per grouping, so the folder counts plus Unassigned equal Total. Uncleared documents and documents
// without a confirmed revision expose no filing metadata and are counted as unassigned.
type DocumentFolders struct {
	By         string           `json:"by"`
	Items      []DocumentFolder `json:"items"`
	Unassigned int64            `json:"unassigned"`
	Total      int64            `json:"total"`
}

var folderGroupings = map[string]struct{ table, column string }{
	"company":  {"suppliers", "supplier_id"},
	"category": {"categories", "category_id"},
}

// folderDocuments mirrors BrowseDocuments' joins so a folder count matches the rows its list returns.
const folderDocuments = `SELECT CASE WHEN d.clearance='cleared' THEN r.%s END AS folder
 FROM invoyis.documents d JOIN invoyis.processing_jobs j ON j.document_id=d.id AND j.archive_id=d.archive_id
 JOIN invoyis.submissions sub ON sub.id=d.submission_id AND sub.archive_id=d.archive_id
 LEFT JOIN invoyis.document_revisions r ON r.archive_id=d.archive_id AND r.document_id=d.id AND r.id=d.current_confirmed_revision_id AND r.kind='confirmed'
 WHERE d.archive_id=$1`

func (s *Service) ListDocumentFolders(ctx context.Context, actor int64, archiveID, by string) (DocumentFolders, error) {
	grouping, ok := folderGroupings[by]
	if !ok {
		return DocumentFolders{}, ErrInvalid
	}
	if !uuidPattern.MatchString(archiveID) {
		return DocumentFolders{}, ErrNotFound
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return DocumentFolders{}, e
	}
	defer tx.Rollback()
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "view"); e != nil {
		if errors.Is(e, ErrForbidden) {
			return DocumentFolders{}, ErrNotFound
		}
		return DocumentFolders{}, e
	}
	docs := fmt.Sprintf(folderDocuments, grouping.column) // column comes from folderGroupings, never from input
	out := DocumentFolders{By: by, Items: []DocumentFolder{}}
	// Active entries are listed even when empty so a new category is visible; archived ones only while they hold documents.
	rows, e := tx.QueryContext(ctx, `WITH docs AS (`+docs+`)
 SELECT f.id,f.display_name,f.archived_at IS NOT NULL,count(docs.folder)
 FROM invoyis.`+grouping.table+` f LEFT JOIN docs ON docs.folder=f.id
 WHERE f.archive_id=$1
 GROUP BY f.id,f.display_name,f.archived_at
 HAVING count(docs.folder)>0 OR f.archived_at IS NULL
 ORDER BY f.display_name,f.id`, archiveID)
	if e != nil {
		return DocumentFolders{}, e
	}
	defer rows.Close()
	for rows.Next() {
		var v DocumentFolder
		if e = rows.Scan(&v.ID, &v.DisplayName, &v.Archived, &v.Count); e != nil {
			return DocumentFolders{}, e
		}
		out.Items = append(out.Items, v)
	}
	if e = rows.Err(); e != nil {
		return DocumentFolders{}, e
	}
	if e = tx.QueryRowContext(ctx, `WITH docs AS (`+docs+`) SELECT count(*) FILTER (WHERE folder IS NULL),count(*) FROM docs`, archiveID).Scan(&out.Unassigned, &out.Total); e != nil {
		return DocumentFolders{}, e
	}
	return out, tx.Commit()
}

// folderExists reports ErrNotFound for a folder filter naming an ID outside this archive. An empty value or "none" passes.
func folderExists(ctx context.Context, tx *sql.Tx, archiveID, table, id string) error {
	if id == "" || id == folderNone {
		return nil
	}
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM invoyis.suppliers WHERE archive_id=$1 AND id=$2)`
	if table == "categories" {
		query = `SELECT EXISTS(SELECT 1 FROM invoyis.categories WHERE archive_id=$1 AND id=$2)`
	}
	if e := tx.QueryRowContext(ctx, query, archiveID, id).Scan(&exists); e != nil {
		return e
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}
