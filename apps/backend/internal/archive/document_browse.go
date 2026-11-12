package archive

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"time"
)

type DocumentFilters struct {
	CategoryID       string `json:"categoryId"`
	PropertyID       string `json:"propertyId"`
	PaperLocationID  string `json:"paperLocationId"`
	AccountReference string `json:"accountReference"`
}
type browseCursor struct {
	Archive  string          `json:"a"`
	Supplier string          `json:"s"`
	Filters  DocumentFilters `json:"f"`
	Date     *string         `json:"d"`
	ID       string          `json:"i"`
}

func parseDocumentFilters(q url.Values, supplier bool) (DocumentFilters, string, error) {
	allowed := map[string]bool{"categoryId": true, "propertyId": true, "paperLocationId": true, "cursor": true}
	if supplier {
		allowed["accountReference"] = true
		delete(allowed, "categoryId")
		delete(allowed, "paperLocationId")
	}
	for k, v := range q {
		if !allowed[k] || len(v) != 1 {
			return DocumentFilters{}, "", ErrInvalid
		}
	}
	f := DocumentFilters{CategoryID: q.Get("categoryId"), PropertyID: q.Get("propertyId"), PaperLocationID: q.Get("paperLocationId"), AccountReference: q.Get("accountReference")}
	for _, id := range []string{f.CategoryID, f.PropertyID, f.PaperLocationID} {
		if id != "" && !uuidPattern.MatchString(id) {
			return f, "", ErrInvalid
		}
	}
	return f, q.Get("cursor"), nil
}
func decodeBrowseCursor(raw, archive, supplier string, f DocumentFilters) (browseCursor, error) {
	if raw == "" {
		return browseCursor{}, nil
	}
	if len(raw) > 2048 {
		return browseCursor{}, ErrInvalid
	}
	data, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return browseCursor{}, ErrInvalid
	}
	var c browseCursor
	if json.Unmarshal(data, &c) != nil || c.Archive != archive || c.Supplier != supplier || c.Filters != f || !uuidPattern.MatchString(c.ID) {
		return browseCursor{}, ErrInvalid
	}
	if c.Date != nil {
		if len(*c.Date) != 10 {
			return browseCursor{}, ErrInvalid
		}
		parsed, e := time.Parse("2006-01-02", *c.Date)
		if e != nil || parsed.Format("2006-01-02") != *c.Date {
			return browseCursor{}, ErrInvalid
		}
	}
	return c, nil
}
func encodeBrowseCursor(c browseCursor) string {
	data, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(data)
}

func (s *Service) BrowseDocuments(ctx context.Context, actor int64, archiveID, supplierID string, f DocumentFilters, cursor string) (DocumentStatuses, error) {
	if !uuidPattern.MatchString(archiveID) || (supplierID != "" && !uuidPattern.MatchString(supplierID)) {
		return DocumentStatuses{}, ErrNotFound
	}
	c, e := decodeBrowseCursor(cursor, archiveID, supplierID, f)
	if e != nil {
		return DocumentStatuses{}, e
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return DocumentStatuses{}, e
	}
	defer tx.Rollback()
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "view"); e != nil {
		if errors.Is(e, ErrForbidden) {
			return DocumentStatuses{}, ErrNotFound
		}
		return DocumentStatuses{}, e
	}
	if supplierID != "" {
		var exists bool
		e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM invoyis.suppliers WHERE archive_id=$1 AND id=$2)`, archiveID, supplierID).Scan(&exists)
		if e != nil {
			return DocumentStatuses{}, e
		}
		if !exists {
			return DocumentStatuses{}, ErrNotFound
		}
	}
	// A confirmed revision is joined by archive, document and revision ID. Drafts cannot affect this result.
	rows, e := tx.QueryContext(ctx, `SELECT d.id,j.state,d.state,d.clearance,j.failure_code,sub.original_filename,
 NULLIF(r.metadata->>'documentType',''),NULLIF(r.metadata->>'title',''),r.issue_date::text,
 NULLIF(r.metadata->>'supplier',''),NULLIF(r.metadata->>'invoiceId',''),NULLIF(r.metadata->>'accountReference',''),
 r.total::text,NULLIF(r.metadata->>'currency',''),r.supplier_id,sp.display_name,
 r.category_id,cat.display_name,r.property_id,prop.display_name,r.paper_location_id,loc.display_name,
 COALESCE(r.reference_scan,false)
 FROM invoyis.documents d JOIN invoyis.processing_jobs j ON j.document_id=d.id AND j.archive_id=d.archive_id
 JOIN invoyis.submissions sub ON sub.id=d.submission_id AND sub.archive_id=d.archive_id
 LEFT JOIN invoyis.document_revisions r ON r.archive_id=d.archive_id AND r.document_id=d.id AND r.id=d.current_confirmed_revision_id AND r.kind='confirmed'
 LEFT JOIN invoyis.suppliers sp ON sp.archive_id=r.archive_id AND sp.id=r.supplier_id
 LEFT JOIN invoyis.categories cat ON cat.archive_id=r.archive_id AND cat.id=r.category_id
 LEFT JOIN invoyis.properties prop ON prop.archive_id=r.archive_id AND prop.id=r.property_id
 LEFT JOIN invoyis.paper_locations loc ON loc.archive_id=r.archive_id AND loc.id=r.paper_location_id
 WHERE d.archive_id=$1 AND ($2::uuid IS NULL OR r.supplier_id=$2::uuid)
 AND ($3::uuid IS NULL OR r.category_id=$3::uuid) AND ($4::uuid IS NULL OR r.property_id=$4::uuid)
 AND ($5::uuid IS NULL OR r.paper_location_id=$5::uuid)
 AND ($6::text IS NULL OR r.metadata->>'accountReference'=$6::text)
 AND ($7::uuid IS NULL OR (CASE WHEN $8::date IS NULL THEN r.issue_date IS NULL AND d.id<$7::uuid ELSE (r.issue_date<$8::date OR r.issue_date IS NULL OR (r.issue_date=$8::date AND d.id<$7::uuid)) END))
 ORDER BY r.issue_date DESC NULLS LAST,d.id DESC LIMIT 21`, archiveID, nullUUID(supplierID), nullUUID(f.CategoryID), nullUUID(f.PropertyID), nullUUID(f.PaperLocationID), nullTextFilter(f.AccountReference), nullUUID(c.ID), c.Date)
	if e != nil {
		return DocumentStatuses{}, e
	}
	defer rows.Close()
	out := DocumentStatuses{Items: []DocumentStatus{}}
	for rows.Next() {
		var v DocumentStatus
		var job, state, clearance string
		var failure sql.NullString
		var date, supplier, number, account, total, currency, supplierID2, supplierName, categoryID, categoryName, propertyID, propertyName, locationID, locationName sql.NullString
		e = rows.Scan(&v.ID, &job, &state, &clearance, &failure, &v.OriginalFilename, &v.ConfirmedType, &v.ConfirmedTitle, &date, &supplier, &number, &account, &total, &currency, &supplierID2, &supplierName, &categoryID, &categoryName, &propertyID, &propertyName, &locationID, &locationName, &v.ReferenceScan)
		if e != nil {
			return DocumentStatuses{}, e
		}
		v.Status = documentStatus(job, state, clearance, failure.String)
		v.hideUnclearedMetadata(clearance)
		if clearance == "cleared" {
			v.IssueDate = nullPtr(date)
			v.PrintedSupplier = nullPtr(supplier)
			v.DocumentNumber = nullPtr(number)
			v.AccountReference = nullPtr(account)
			v.Total = nullPtr(total)
			v.Currency = nullPtr(currency)
			v.SupplierID = nullPtr(supplierID2)
			v.SupplierName = nullPtr(supplierName)
			v.CategoryID = nullPtr(categoryID)
			v.CategoryName = nullPtr(categoryName)
			v.PropertyID = nullPtr(propertyID)
			v.PropertyName = nullPtr(propertyName)
			v.PaperLocationID = nullPtr(locationID)
			v.PaperLocationName = nullPtr(locationName)
		} else {
			v.ReferenceScan = false
		}
		out.Items = append(out.Items, v)
	}
	if e = rows.Err(); e != nil {
		return DocumentStatuses{}, e
	}
	if len(out.Items) > 20 {
		out.Items = out.Items[:20]
		last := out.Items[19]
		out.NextCursor = encodeBrowseCursor(browseCursor{Archive: archiveID, Supplier: supplierID, Filters: f, Date: last.IssueDate, ID: last.ID})
	}
	return out, tx.Commit()
}
func nullUUID(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func nullTextFilter(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func nullPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}
