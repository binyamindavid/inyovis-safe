package archive

import (
	"context"
	"errors"
	"sort"
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
	if !uuidPattern.MatchString(archiveID) {
		return StorageReport{}, ErrNotFound
	}
	if by != "category" && by != "company" && by != "format" && by != "type" || mode != "overview" && mode != "review" {
		return StorageReport{}, ErrInvalid
	}
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return StorageReport{}, err
	}
	defer tx.Rollback()
	role, _, err := s.membership(ctx, tx, archiveID, actor, "view")
	if errors.Is(err, ErrForbidden) {
		return StorageReport{}, ErrNotFound
	}
	if err != nil {
		return StorageReport{}, err
	}
	// Candidate membership is checked before reading any rows; viewers cannot infer
	// shredding candidates or their aggregate bytes.
	if mode == "review" && role != "admin" {
		return StorageReport{}, ErrNotFound
	}
	rows, err := tx.QueryContext(ctx, `SELECT o.id,o.byte_count,d.id,CASE WHEN d.clearance='cleared' THEN COALESCE(NULLIF(r.metadata->>'title',''),sub.original_filename) ELSE sub.original_filename END,
 CASE WHEN d.clearance='cleared' THEN COALESCE(r.category_id::text,'') ELSE '' END,
 CASE WHEN d.clearance='cleared' THEN COALESCE(cat.display_name,'') ELSE '' END,
 CASE WHEN d.clearance='cleared' THEN COALESCE(r.supplier_id::text,'') ELSE '' END,
 CASE WHEN d.clearance='cleared' THEN COALESCE(sp.display_name,'') ELSE '' END,
 CASE WHEN d.clearance='cleared' THEN COALESCE(r.metadata->>'documentType','') ELSE '' END,
 CASE WHEN u.media_type='application/pdf' AND EXISTS (SELECT 1 FROM invoyis.submissions verified_sub JOIN invoyis.documents verified_doc ON verified_doc.archive_id=verified_sub.archive_id AND verified_doc.submission_id=verified_sub.id JOIN invoyis.processing_jobs verified_job ON verified_job.archive_id=verified_doc.archive_id AND verified_job.document_id=verified_doc.id WHERE verified_sub.archive_id=o.archive_id AND verified_sub.stored_object_id=o.id AND verified_job.state='succeeded') THEN 'PDF' ELSE '' END
 FROM invoyis.stored_objects o
 JOIN invoyis.submissions sub ON sub.archive_id=o.archive_id AND sub.stored_object_id=o.id
 JOIN invoyis.documents d ON d.archive_id=sub.archive_id AND d.submission_id=sub.id
 JOIN invoyis.upload_sessions u ON u.archive_id=o.archive_id AND u.id=o.upload_session_id
 JOIN invoyis.processing_jobs j ON j.archive_id=d.archive_id AND j.document_id=d.id
 LEFT JOIN invoyis.document_revisions r ON r.archive_id=d.archive_id AND r.document_id=d.id AND r.id=d.current_confirmed_revision_id AND r.kind='confirmed'
 LEFT JOIN invoyis.categories cat ON cat.archive_id=r.archive_id AND cat.id=r.category_id
 LEFT JOIN invoyis.suppliers sp ON sp.archive_id=r.archive_id AND sp.id=r.supplier_id
 WHERE o.archive_id=$1 ORDER BY o.id,d.id`, archiveID)
	if err != nil {
		return StorageReport{}, err
	}
	refs := []storageReference{}
	for rows.Next() {
		var r storageReference
		if err = rows.Scan(&r.objectID, &r.bytes, &r.documentID, &r.name, &r.categoryID, &r.categoryName, &r.supplierID, &r.supplierName, &r.documentType, &r.format); err != nil {
			break
		}
		refs = append(refs, r)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return StorageReport{}, err
	}
	if err = tx.Commit(); err != nil {
		return StorageReport{}, err
	}
	if mode == "review" {
		for i := range refs {
			var details RetentionDetails
			details, err = s.RetentionDetails(ctx, actor, archiveID, refs[i].documentID)
			if err != nil {
				return StorageReport{}, err
			}
			refs[i].eligible = details.ReviewBlocker == ""
		}
	}
	return aggregateStorage(refs, by, mode), nil
}
