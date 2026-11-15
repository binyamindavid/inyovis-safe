package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/retention"
)

// Storage counts stored originals, not previews, exports, or backups. An original
// with references in different filing groups belongs to Shared originals for
// that dimension; its document references remain available in the drill-down.
type StorageDocument struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	SharedWithRetained bool   `json:"sharedWithRetained,omitempty"`
}
type StorageGroup struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Bytes         int64             `json:"bytes,string"`
	DocumentCount int               `json:"documentCount"`
	Percentage    float64           `json:"percentage"`
	Documents     []StorageDocument `json:"documents,omitempty"`
}
type StorageReport struct {
	By            string         `json:"by"`
	Mode          string         `json:"mode"`
	Bytes         int64          `json:"bytes,string"`
	OriginalCount int            `json:"originalCount"`
	DocumentCount int            `json:"documentCount"`
	Groups        []StorageGroup `json:"groups"`
}
type storageReference struct {
	objectID, documentID, name, categoryID, categoryName, supplierID, supplierName, documentType, format string
	bytes                                                                                                int64
	eligible                                                                                             bool
	candidate                                                                                            bool
}

func storageKey(r storageReference, by string) (string, string) {
	switch by {
	case "category":
		return r.categoryID, r.categoryName
	case "company":
		return r.supplierID, r.supplierName
	case "format":
		return r.format, r.format
	default:
		return r.documentType, r.documentType
	}
}

func aggregateStorage(refs []storageReference, by, mode string) StorageReport {
	out := StorageReport{By: by, Mode: mode, Groups: []StorageGroup{}}
	objects := map[string][]storageReference{}
	for _, r := range refs {
		objects[r.objectID] = append(objects[r.objectID], r)
	}
	groups := map[string]*StorageGroup{}
	for _, all := range objects {
		included := make([]storageReference, 0, len(all))
		for _, r := range all {
			if mode != "review" || r.eligible {
				included = append(included, r)
			}
		}
		if len(included) == 0 {
			continue
		}
		out.Bytes += all[0].bytes
		out.OriginalCount++
		out.DocumentCount += len(included)
		keys := map[string]string{}
		for _, r := range included {
			id, name := storageKey(r, by)
			keys[id] = name
		}
		id, name := "none", "Unassigned"
		if len(keys) > 1 {
			id, name = "shared", "Shared originals"
		} else {
			for k, n := range keys {
				if k != "" {
					id, name = k, n
					if name == "" {
						name = k
					}
				}
			}
		}
		g := groups[id]
		if g == nil {
			g = &StorageGroup{ID: id, Name: name, Documents: []StorageDocument{}}
			groups[id] = g
		}
		g.Bytes += all[0].bytes
		g.DocumentCount += len(included)
		retained := false
		if mode == "review" {
			for _, r := range all {
				if !r.eligible {
					retained = true
				}
			}
		}
		for _, r := range included {
			g.Documents = append(g.Documents, StorageDocument{ID: r.documentID, Name: r.name, SharedWithRetained: retained})
		}
	}
	for _, g := range groups {
		if out.Bytes > 0 {
			g.Percentage = 100 * float64(g.Bytes) / float64(out.Bytes)
		}
		sort.Slice(g.Documents, func(i, j int) bool { return g.Documents[i].ID < g.Documents[j].ID })
		out.Groups = append(out.Groups, *g)
	}
	sort.Slice(out.Groups, func(i, j int) bool {
		if out.Groups[i].Bytes == out.Groups[j].Bytes {
			return out.Groups[i].Name < out.Groups[j].Name
		}
		return out.Groups[i].Bytes > out.Groups[j].Bytes
	})
	return out
}

func (s *Service) StorageReport(ctx context.Context, actor int64, archiveID, by, mode string) (StorageReport, error) {
	refs, err := s.storageReferences(ctx, actor, archiveID, by, mode, nil)
	if err != nil {
		return StorageReport{}, err
	}
	report := aggregateStorage(refs, by, mode)
	// The headline remains bounded as the archive grows. Document references are
	// requested only when a ranked group is opened.
	for i := range report.Groups {
		report.Groups[i].Documents = nil
	}
	return report, nil
}

type StorageDocumentPage struct {
	Items         []StorageDocument `json:"items"`
	Bytes         int64             `json:"bytes,string"`
	DocumentCount int               `json:"documentCount"`
	NextCursor    string            `json:"nextCursor,omitempty"`
}

func (s *Service) StorageGroupDocuments(ctx context.Context, actor int64, archiveID, by, mode, group, cursor string) (StorageDocumentPage, error) {
	if group == "" || cursor != "" && !uuidPattern.MatchString(cursor) {
		return StorageDocumentPage{}, ErrInvalid
	}
	refs, err := s.storageReferences(ctx, actor, archiveID, by, mode, nil)
	if err != nil {
		return StorageDocumentPage{}, err
	}
	report := aggregateStorage(refs, by, mode)
	for _, g := range report.Groups {
		if g.ID != group {
			continue
		}
		out := StorageDocumentPage{Items: []StorageDocument{}, Bytes: g.Bytes, DocumentCount: g.DocumentCount}
		for _, doc := range g.Documents {
			if cursor != "" && doc.ID <= cursor {
				continue
			}
			if len(out.Items) == 20 {
				out.NextCursor = out.Items[len(out.Items)-1].ID
				break
			}
			out.Items = append(out.Items, doc)
		}
		return out, nil
	}
	return StorageDocumentPage{}, ErrNotFound
}

// storageReferences uses one repeatable-read snapshot for filing, original
// identity, current retention inputs, evaluations and holds. The hook lets a
// regression commit a correction after the filing rows have been read.
func (s *Service) storageReferences(ctx context.Context, actor int64, archiveID, by, mode string, afterMetadataRead func()) ([]storageReference, error) {
	if !uuidPattern.MatchString(archiveID) {
		return nil, ErrNotFound
	}
	if by != "category" && by != "company" && by != "format" && by != "type" || mode != "overview" && mode != "review" {
		return nil, ErrInvalid
	}
	tx, err := s.transactionWithIsolation(ctx, actor, archiveID, sql.LevelRepeatableRead)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	role, _, err := s.membership(ctx, tx, archiveID, actor, "view")
	if errors.Is(err, ErrForbidden) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// Candidate membership is checked before reading any rows; viewers cannot infer
	// shredding candidates or their aggregate bytes.
	if mode == "review" && role != "admin" {
		return nil, ErrNotFound
	}
	now := s.Now()
	candidateSQL := "false"
	if mode == "review" {
		candidateSQL = `d.state='ready' AND d.clearance='cleared' AND d.current_confirmed_revision_id IS NOT NULL AND EXISTS (SELECT 1 FROM invoyis.retention_evaluations ev WHERE ev.archive_id=d.archive_id AND ev.document_id=d.id)`
	}
	// The current intake accepts only PDFs. Successful processing verifies the file.
	// Upload sessions are private to the uploader and must not filter archive usage.
	rows, err := tx.QueryContext(ctx, `SELECT o.id,o.byte_count,d.id,CASE WHEN d.clearance='cleared' THEN COALESCE(NULLIF(r.metadata->>'title',''),sub.original_filename) ELSE sub.original_filename END,
 CASE WHEN d.clearance='cleared' THEN COALESCE(r.category_id::text,'') ELSE '' END,
 CASE WHEN d.clearance='cleared' THEN COALESCE(cat.display_name,'') ELSE '' END,
 CASE WHEN d.clearance='cleared' THEN COALESCE(r.supplier_id::text,'') ELSE '' END,
 CASE WHEN d.clearance='cleared' THEN COALESCE(sp.display_name,'') ELSE '' END,
 CASE WHEN d.clearance='cleared' THEN COALESCE(r.metadata->>'documentType','') ELSE '' END,
 CASE WHEN EXISTS (SELECT 1 FROM invoyis.submissions verified_sub JOIN invoyis.documents verified_doc ON verified_doc.archive_id=verified_sub.archive_id AND verified_doc.submission_id=verified_sub.id JOIN invoyis.processing_jobs verified_job ON verified_job.archive_id=verified_doc.archive_id AND verified_job.document_id=verified_doc.id WHERE verified_sub.archive_id=o.archive_id AND verified_sub.stored_object_id=o.id AND verified_job.state='succeeded') THEN 'PDF' ELSE '' END,
 `+candidateSQL+`
 FROM invoyis.stored_objects o
 JOIN invoyis.submissions sub ON sub.archive_id=o.archive_id AND sub.stored_object_id=o.id
 JOIN invoyis.documents d ON d.archive_id=sub.archive_id AND d.submission_id=sub.id
 LEFT JOIN invoyis.document_revisions r ON r.archive_id=d.archive_id AND r.document_id=d.id AND r.id=d.current_confirmed_revision_id AND r.kind='confirmed'
 LEFT JOIN invoyis.categories cat ON cat.archive_id=r.archive_id AND cat.id=r.category_id
 LEFT JOIN invoyis.suppliers sp ON sp.archive_id=r.archive_id AND sp.id=r.supplier_id
 WHERE o.archive_id=$1 ORDER BY o.id,d.id`, archiveID)
	if err != nil {
		return nil, err
	}
	refs := []storageReference{}
	for rows.Next() {
		var r storageReference
		if err = rows.Scan(&r.objectID, &r.bytes, &r.documentID, &r.name, &r.categoryID, &r.categoryName, &r.supplierID, &r.supplierName, &r.documentType, &r.format, &r.candidate); err != nil {
			break
		}
		refs = append(refs, r)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if afterMetadataRead != nil {
		afterMetadataRead()
	}
	if mode == "review" {
		var catalogue *retentionCatalogue
		for _, ref := range refs {
			if ref.candidate {
				catalogue, err = loadRetentionCatalogue(ctx, tx)
				break
			}
		}
		if err != nil {
			return nil, err
		}
		for i := range refs {
			if !refs[i].candidate {
				continue
			}
			refs[i].eligible, err = s.storageReviewEligible(ctx, tx, archiveID, refs[i].documentID, now, catalogue)
			if err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return refs, nil
}

// The dashboard needs only the current decision and active-hold bit. It uses
// the same candidate fingerprint and blocker rules as manual review, without
// loading historical decisions, released holds, or reminder details.
func (s *Service) storageReviewEligible(ctx context.Context, tx *sql.Tx, archiveID, documentID string, now time.Time, catalogue *retentionCatalogue) (bool, error) {
	c, err := s.readRetentionCandidateWithCatalogue(ctx, tx, archiveID, documentID, catalogue)
	if err != nil || !c.eligible {
		return false, err
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT outcome FROM invoyis.retention_evaluations WHERE archive_id=$1 AND document_id=$2 AND fingerprint=$3`, archiveID, documentID, c.fingerprint).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var outcome retention.Outcome
	if err = json.Unmarshal(raw, &outcome); err != nil {
		return false, err
	}
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM invoyis.document_holds WHERE archive_id=$1 AND document_id=$2 AND released_at IS NULL)`, archiveID, documentID).Scan(&active)
	if err != nil {
		return false, err
	}
	current := CurrentRetentionResult{State: "current", Snapshot: &RetentionSnapshot{Outcome: outcome}}
	activeCount := 0
	if active {
		activeCount = 1
	}
	return reviewBlocker(c.input.Facts.Kind, "admin", c.eligible, current, activeCount, now) == "", nil
}
