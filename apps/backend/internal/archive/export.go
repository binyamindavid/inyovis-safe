package archive

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const maxExportDocuments = 20
const maxExportOriginalBytes int64 = 100 << 20
const maxExportBundleBytes int64 = 110 << 20
const exportLifetime = 15 * time.Minute

// Bound simultaneous temporary ZIPs and large bundle reads in each API process.
var exportBuildSlots = make(chan struct{}, 2)
var exportDownloadSlots = make(chan struct{}, 4)

type exportItem struct {
	DocumentID string         `json:"documentId"`
	Entry      string         `json:"entry"`
	Key        string         `json:"key"`
	Digest     string         `json:"digest"`
	Size       int64          `json:"size"`
	Filename   string         `json:"filename"`
	Metadata   ReviewMetadata `json:"metadata"`
	Filing     ExportFiling   `json:"filing"`
}

type ExportReceipt struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func safePDFName(name string) string {
	name = strings.TrimSuffix(name, ".pdf")
	name = strings.TrimSuffix(name, ".PDF")
	var out strings.Builder
	for _, r := range name {
		if out.Len() >= 72 {
			break
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			out.WriteRune(r)
		} else if r == ' ' {
			out.WriteByte('_')
		}
	}
	base := strings.Trim(out.String(), "._-")
	if base == "" {
		base = "document"
	}
	return base + ".pdf"
}

func (s *Service) originalSource(ctx context.Context, actor int64, archiveID, documentID string) (exportItem, error) {
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) {
		return exportItem{}, ErrNotFound
	}
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return exportItem{}, err
	}
	defer tx.Rollback()
	if _, _, err = s.membership(ctx, tx, archiveID, actor, "view"); err != nil {
		if errors.Is(err, ErrForbidden) {
			return exportItem{}, ErrNotFound
		}
		return exportItem{}, err
	}
	var item exportItem
	var state, clearance string
	err = tx.QueryRowContext(ctx, `SELECT d.state,d.clearance,sub.original_filename,o.storage_key,o.sha256,o.byte_count
 FROM invoyis.documents d JOIN invoyis.submissions sub ON sub.id=d.submission_id
 JOIN invoyis.stored_objects o ON o.id=sub.stored_object_id WHERE d.archive_id=$1 AND d.id=$2`, archiveID, documentID).
		Scan(&state, &clearance, &item.Filename, &item.Key, &item.Digest, &item.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return exportItem{}, ErrNotFound
	}
	if err != nil {
		return exportItem{}, err
	}
	if clearance != "cleared" || state != "needs_review" && state != "ready" {
		return exportItem{}, ErrForbidden
	}
	item.DocumentID = documentID
	return item, tx.Commit()
}

func (s *Service) DownloadOriginal(ctx context.Context, actor int64, archiveID, documentID string) ([]byte, string, error) {
	item, err := s.originalSource(ctx, actor, archiveID, documentID)
	if err != nil {
		return nil, "", err
	}
	if s.Storage == nil || item.Size <= 0 || item.Size > 25<<20 {
		return nil, "", ErrIntegrity
	}
	data, err := s.Storage.Read(ctx, item.Key, item.Size)
	if err != nil {
		return nil, "", err
	}
	if !matchesOriginal(data, item) {
		return nil, "", ErrIntegrity
	}
	// Recheck after storage I/O so a clearance or membership change during the read wins.
	if _, err = s.originalSource(ctx, actor, archiveID, documentID); err != nil {
		return nil, "", err
	}
	return data, safePDFName(item.Filename), nil
}

func matchesOriginal(data []byte, item exportItem) bool {
	if int64(len(data)) != item.Size {
		return false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == item.Digest
}

func (s *Service) exportSnapshot(ctx context.Context, actor int64, archiveID string) ([]exportItem, error) {
	if !uuidPattern.MatchString(archiveID) {
		return nil, ErrNotFound
	}
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, _, err = s.membership(ctx, tx, archiveID, actor, "bulk_export"); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.id,sub.original_filename,o.storage_key,o.sha256,o.byte_count,r.metadata,
 COALESCE(r.supplier_id::text,''),COALESCE(s.display_name,''),
 COALESCE(r.category_id::text,''),COALESCE(c.display_name,''),
 COALESCE(r.property_id::text,''),COALESCE(p.display_name,''),
 COALESCE(r.paper_location_id::text,''),COALESCE(l.display_name,''),r.reference_scan
 FROM invoyis.documents d JOIN invoyis.submissions sub ON sub.id=d.submission_id
 JOIN invoyis.stored_objects o ON o.id=sub.stored_object_id
 JOIN invoyis.document_revisions r ON r.id=d.current_confirmed_revision_id AND r.archive_id=d.archive_id AND r.document_id=d.id AND r.kind='confirmed'
 LEFT JOIN invoyis.suppliers s ON s.archive_id=d.archive_id AND s.id=r.supplier_id
 LEFT JOIN invoyis.categories c ON c.archive_id=d.archive_id AND c.id=r.category_id
 LEFT JOIN invoyis.properties p ON p.archive_id=d.archive_id AND p.id=r.property_id
 LEFT JOIN invoyis.paper_locations l ON l.archive_id=d.archive_id AND l.id=r.paper_location_id
 WHERE d.archive_id=$1 AND d.state='ready' AND d.clearance='cleared'
 ORDER BY d.created_at,d.id LIMIT $2`, archiveID, maxExportDocuments+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []exportItem{}
	var total int64
	for rows.Next() {
		var item exportItem
		var raw []byte
		f := &item.Filing
		if err = rows.Scan(&item.DocumentID, &item.Filename, &item.Key, &item.Digest, &item.Size, &raw,
			&f.SupplierID, &f.SupplierName, &f.CategoryID, &f.CategoryName,
			&f.PropertyID, &f.PropertyName, &f.PaperLocationID, &f.PaperLocationName, &f.ReferenceScan); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &item.Metadata); err != nil {
			return nil, err
		}
		if item.Size <= 0 || item.Size > 25<<20 {
			return nil, ErrIntegrity
		}
		total += item.Size
		if len(items) >= maxExportDocuments || total > maxExportOriginalBytes {
			return nil, ErrInvalid
		}
		item.Entry = "originals/" + item.DocumentID + "/" + safePDFName(item.Filename)
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, tx.Commit()
}

func writeExportZIP(file *os.File, items []exportItem, read func(exportItem) ([]byte, error)) (int64, string, error) {
	writer := zip.NewWriter(file)
	index, manifest := new(strings.Builder), new(strings.Builder)
	csvIndex, csvManifest := csv.NewWriter(index), csv.NewWriter(manifest)
	if err := csvIndex.Write(append([]string{"bundle_entry"}, IndexColumns...)); err != nil {
		return 0, "", err
	}
	if err := csvManifest.Write([]string{"bundle_entry", "sha256"}); err != nil {
		return 0, "", err
	}
	for _, item := range items {
		data, err := read(item)
		if err != nil {
			return 0, "", err
		}
		if !matchesOriginal(data, item) {
			return 0, "", ErrIntegrity
		}
		header := &zip.FileHeader{Name: item.Entry, Method: zip.Store}
		header.SetMode(0600)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return 0, "", err
		}
		if _, err = entry.Write(data); err != nil {
			return 0, "", err
		}
		if err = csvIndex.Write(append([]string{item.Entry}, IndexRow(item.DocumentID, item.Metadata, item.Filing)...)); err != nil {
			return 0, "", err
		}
		if err = csvManifest.Write([]string{item.Entry, item.Digest}); err != nil {
			return 0, "", err
		}
	}
	csvIndex.Flush()
	csvManifest.Flush()
	if err := csvIndex.Error(); err != nil {
		return 0, "", err
	}
	if err := csvManifest.Error(); err != nil {
		return 0, "", err
	}
	format, err := json.Marshal(struct {
		SchemaVersion int      `json:"schemaVersion"`
		IndexColumns  []string `json:"indexColumns"`
	}{2, append([]string{"bundle_entry"}, IndexColumns...)})
	if err != nil {
		return 0, "", err
	}
	for _, aux := range []struct{ name, body string }{{"index.csv", index.String()}, {"sha256.csv", manifest.String()}, {"format.json", string(format)}} {
		entry, err := writer.Create(aux.name)
		if err != nil {
			return 0, "", err
		}
		if _, err = io.WriteString(entry, aux.body); err != nil {
			return 0, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return 0, "", err
	}
	info, err := file.Stat()
	if err != nil {
		return 0, "", err
	}
	if info.Size() > maxExportBundleBytes {
		return 0, "", ErrInvalid
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return 0, "", err
	}
	h := sha256.New()
	if _, err = io.Copy(h, file); err != nil {
		return 0, "", err
	}
	return info.Size(), hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Service) CreateExport(ctx context.Context, actor int64, archiveID string) (ExportReceipt, error) {
	select {
	case exportBuildSlots <- struct{}{}:
		defer func() { <-exportBuildSlots }()
	case <-ctx.Done():
		return ExportReceipt{}, ctx.Err()
	}
	if s.Bundles == nil || s.Storage == nil {
		return ExportReceipt{}, ErrIntegrity
	}
	items, err := s.exportSnapshot(ctx, actor, archiveID)
	if err != nil {
		return ExportReceipt{}, err
	}
	id, err := newID()
	if err != nil {
		return ExportReceipt{}, err
	}
	key := "exports/" + archiveID + "/" + id + ".zip"
	expires := s.Now().Add(exportLifetime).UTC()
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return ExportReceipt{}, err
	}
	if _, _, err = s.membership(ctx, tx, archiveID, actor, "bulk_export"); err != nil {
		tx.Rollback()
		return ExportReceipt{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO invoyis.archive_exports(id,archive_id,storage_key,state,expires_at) VALUES($1,$2,$3,'building',$4)`, id, archiveID, key, expires)
	if err != nil {
		tx.Rollback()
		return ExportReceipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return ExportReceipt{}, err
	}
	defer func() {
		if err != nil {
			s.markExportFailed(context.Background(), actor, archiveID, id)
			_ = s.Bundles.DeleteBundle(context.Background(), key)
		}
	}()
	file, e := os.CreateTemp("", "invoyis-export-*.zip")
	if e != nil {
		err = e
		return ExportReceipt{}, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, digest, e := writeExportZIP(file, items, func(item exportItem) ([]byte, error) { return s.Storage.Read(ctx, item.Key, item.Size) })
	if e != nil {
		err = e
		return ExportReceipt{}, err
	}
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		err = e
		return ExportReceipt{}, err
	}
	if e = s.Bundles.PutBundle(ctx, key, file, size); e != nil {
		err = e
		return ExportReceipt{}, err
	}
	// Only publish a complete bundle while the grant and every included clearance still hold.
	if e = s.publishExport(ctx, actor, archiveID, id, items, size, digest); e != nil {
		err = e
		return ExportReceipt{}, err
	}
	return ExportReceipt{ID: id, ExpiresAt: expires}, nil
}

func (s *Service) markExportFailed(ctx context.Context, actor int64, archiveID, id string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return
	}
	defer tx.Rollback()
	_, _ = tx.ExecContext(ctx, `UPDATE invoyis.archive_exports SET state='failed' WHERE id=$1 AND state='building'`, id)
	_ = tx.Commit()
}

func (s *Service) publishExport(ctx context.Context, actor int64, archiveID, id string, items []exportItem, size int64, digest string) error {
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, _, err = s.membership(ctx, tx, archiveID, actor, "bulk_export"); err != nil {
		return err
	}
	if err = checkExportClearance(ctx, tx, archiveID, items); err != nil {
		return err
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE invoyis.archive_exports SET state='ready',items=$2,bundle_sha256=$3,byte_count=$4 WHERE id=$1 AND state='building' AND expires_at>now()`, id, raw, digest, size)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return ErrForbidden
	}
	return tx.Commit()
}

func checkExportClearance(ctx context.Context, tx *sql.Tx, archiveID string, items []exportItem) error {
	for _, item := range items {
		var state, clearance string
		err := tx.QueryRowContext(ctx, `SELECT state,clearance FROM invoyis.documents WHERE archive_id=$1 AND id=$2`, archiveID, item.DocumentID).Scan(&state, &clearance)
		if err != nil {
			return ErrForbidden
		}
		if state != "ready" || clearance != "cleared" {
			return ErrForbidden
		}
	}
	return nil
}

func (s *Service) DownloadExport(ctx context.Context, actor int64, archiveID, id string) ([]byte, error) {
	select {
	case exportDownloadSlots <- struct{}{}:
		defer func() { <-exportDownloadSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(id) {
		return nil, ErrNotFound
	}
	if s.Bundles == nil {
		return nil, ErrIntegrity
	}
	lookup := func() (string, string, int64, []exportItem, error) {
		tx, err := s.transaction(ctx, actor, archiveID)
		if err != nil {
			return "", "", 0, nil, err
		}
		defer tx.Rollback()
		if _, _, err = s.membership(ctx, tx, archiveID, actor, "bulk_export"); err != nil {
			return "", "", 0, nil, err
		}
		var key, digest string
		var size int64
		var raw []byte
		err = tx.QueryRowContext(ctx, `SELECT storage_key,bundle_sha256,byte_count,items FROM invoyis.archive_exports WHERE archive_id=$1 AND id=$2 AND state='ready' AND expires_at>now()`, archiveID, id).Scan(&key, &digest, &size, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", 0, nil, ErrNotFound
		}
		if err != nil {
			return "", "", 0, nil, err
		}
		var items []exportItem
		if err = json.Unmarshal(raw, &items); err != nil {
			return "", "", 0, nil, err
		}
		if err = checkExportClearance(ctx, tx, archiveID, items); err != nil {
			return "", "", 0, nil, err
		}
		return key, digest, size, items, tx.Commit()
	}
	key, digest, size, _, err := lookup()
	if err != nil {
		return nil, err
	}
	if size <= 0 || size > maxExportBundleBytes {
		return nil, ErrIntegrity
	}
	data, err := s.Bundles.Read(ctx, key, size)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != size || hex.EncodeToString(sum[:]) != digest {
		return nil, ErrIntegrity
	}
	// A second check closes the window while reading from object storage.
	if _, _, _, _, err = lookup(); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Service) CleanupExports(ctx context.Context) error {
	if s.Bundles == nil {
		return nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,storage_key FROM invoyis.export_cleanup_candidates(20)`)
	if err != nil {
		return err
	}
	type candidate struct{ id, key string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.key); err != nil {
			break
		}
		candidates = append(candidates, c)
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if err = s.Bundles.DeleteBundle(ctx, c.key); err != nil {
			return err
		}
		if _, err = s.DB.ExecContext(ctx, `SELECT invoyis.export_cleanup_done($1)`, c.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RunExportCleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.CleanupExports(ctx)
		}
	}
}

func (s *Service) getOriginal(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	data, name, err := s.DownloadOriginal(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
func (s *Service) createExport(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	receipt, err := s.CreateExport(r.Context(), actor, r.PathValue("archiveId"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, 201, requestID, receipt)
}
func (s *Service) getExport(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	data, err := s.DownloadExport(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="archive-export.zip"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
