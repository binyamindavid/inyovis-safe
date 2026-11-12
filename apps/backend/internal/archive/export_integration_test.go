package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"
)

type fakeBundles struct{ objects map[string][]byte }

func (f *fakeBundles) Read(_ context.Context, key string, cap int64) ([]byte, error) {
	b, ok := f.objects[key]
	if !ok {
		return nil, ErrObjectMissing
	}
	if int64(len(b)) > cap {
		return nil, ErrInvalid
	}
	return append([]byte(nil), b...), nil
}
func (f *fakeBundles) PutBundle(_ context.Context, key string, r io.Reader, size int64) error {
	b, e := io.ReadAll(r)
	if e != nil {
		return e
	}
	if int64(len(b)) != size {
		return ErrIntegrity
	}
	f.objects[key] = b
	return nil
}
func (f *fakeBundles) DeleteBundle(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

func TestExportDownloadPermissionsIntegrityAndLifecycle(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	bundles := &fakeBundles{objects: map[string][]byte{}}
	s.Storage, s.Bundles = store, bundles
	a, err := s.Create(ctx, actor, "personal", "Export test", "")
	if err != nil {
		t.Fatal(err)
	}
	viewer := addAccount(t, owner, "export-viewer@example.test")
	if _, err = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a.ID, viewer); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	data := [][]byte{}
	for _, kind := range []string{TypeLease, TypeCourtCorrespondence} {
		id, b, _ := clearedUpload(t, s, owner, store, actor, a.ID, kind)
		ids = append(ids, id)
		data = append(data, b)
		meta := ReviewMetadata{DocumentType: kind, Title: "Confirmed " + kind, Reference: "00042-AB", Sender: "Élodie / דוד", TaxLines: []TaxLine{}}
		if _, err = write(t, s, actor, a.ID, id, "confirm", 0, meta); err != nil {
			t.Fatal(err)
		}
	}
	// A viewer can read a cleared original even without the bulk grant.
	got, name, err := s.DownloadOriginal(ctx, viewer, a.ID, ids[0])
	if err != nil || !bytes.Equal(got, data[0]) || name != "document.pdf" {
		t.Fatalf("viewer original %q %v", name, err)
	}
	if _, err = s.CreateExport(ctx, viewer, a.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer export %v", err)
	}
	if _, err = s.CreateExport(ctx, actor, a.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin without grant %v", err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.archive_memberships SET can_bulk_export=true WHERE archive_id=$1 AND account_id=$2`, a.ID, actor); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.CreateExport(ctx, actor, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := s.DownloadExport(ctx, actor, a.ID, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	z, e := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if e != nil {
		t.Fatal(e)
	}
	contents := map[string][]byte{}
	for _, file := range z.File {
		r, e := file.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		contents[file.Name] = b
	}
	var format struct {
		SchemaVersion int      `json:"schemaVersion"`
		IndexColumns  []string `json:"indexColumns"`
	}
	if e := json.Unmarshal(contents["format.json"], &format); e != nil || format.SchemaVersion != 2 || !reflect.DeepEqual(format.IndexColumns, append([]string{"bundle_entry"}, IndexColumns...)) {
		t.Fatalf("format metadata: %+v %v", format, e)
	}
	index, e := csv.NewReader(bytes.NewReader(contents["index.csv"])).ReadAll()
	if e != nil {
		t.Fatal(e)
	}
	manifest, e := csv.NewReader(bytes.NewReader(contents["sha256.csv"])).ReadAll()
	if e != nil {
		t.Fatal(e)
	}
	if len(index) != 3 || len(manifest) != 3 || index[1][0] == index[2][0] {
		t.Fatalf("index/manifest rows %v %v", index, manifest)
	}
	legacyHeader := []string{"bundle_entry", "document_id", "document_type", "title", "sender", "document_date", "reference", "notes", "supplier", "invoice_id", "account_reference", "currency", "subtotal", "tax", "total"}
	if !reflect.DeepEqual(index[0][:len(legacyHeader)], legacyHeader) || !reflect.DeepEqual(index[0][len(legacyHeader):], []string{"filing_supplier_id", "filing_supplier_name", "filing_category_id", "filing_category_name", "filing_property_id", "filing_property_name", "filing_paper_location_id", "filing_paper_location_name", "reference_scan"}) {
		t.Fatalf("index header %v", index[0])
	}
	for i := range ids {
		entry := index[i+1][0]
		if !bytes.Equal(contents[entry], data[i]) || index[i+1][6] != "00042-AB" || index[i+1][8] != "" {
			t.Fatalf("row %v", index[i+1])
		}
		sum := sha256.Sum256(data[i])
		if manifest[i+1][0] != entry || manifest[i+1][1] != hex.EncodeToString(sum[:]) {
			t.Fatalf("manifest %v", manifest[i+1])
		}
	}
	// A later draft cannot replace confirmed export metadata.
	_, err = write(t, s, actor, a.ID, ids[0], "draft", 1, ReviewMetadata{DocumentType: TypeLease, Title: "Draft only", TaxLines: []TaxLine{}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateExport(ctx, actor, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := s.DownloadExport(ctx, actor, a.ID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondZIP, err := zip.NewReader(bytes.NewReader(secondData), int64(len(secondData)))
	if err != nil {
		t.Fatal(err)
	}
	var secondCSV []byte
	for _, entry := range secondZIP.File {
		if entry.Name == "index.csv" {
			r, e := entry.Open()
			if e != nil {
				t.Fatal(e)
			}
			secondCSV, e = io.ReadAll(r)
			r.Close()
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	if !bytes.Contains(secondCSV, []byte("Confirmed lease")) || bytes.Contains(secondCSV, []byte("Draft only")) {
		t.Fatal("draft leaked into export")
	}
	if _, err = owner.Exec(`UPDATE invoyis.archive_memberships SET can_bulk_export=false WHERE archive_id=$1 AND account_id=$2`, a.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DownloadExport(ctx, actor, a.ID, receipt.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked grant %v", err)
	}
	if _, err = owner.Exec(`DELETE FROM invoyis.archive_memberships WHERE archive_id=$1 AND account_id=$2`, a.ID, viewer); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.DownloadOriginal(ctx, viewer, a.ID, ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked membership original %v", err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.archive_memberships SET can_bulk_export=true WHERE archive_id=$1 AND account_id=$2`, a.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.documents SET clearance='blocked' WHERE id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DownloadExport(ctx, actor, a.ID, receipt.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("blocked bundle %v", err)
	}
	if _, _, err = s.DownloadOriginal(ctx, actor, a.ID, ids[0]); !errors.Is(err, ErrForbidden) {
		t.Fatalf("blocked original %v", err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.documents SET clearance='pending' WHERE id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DownloadExport(ctx, actor, a.ID, receipt.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("pending bundle %v", err)
	}
	if _, _, err = s.DownloadOriginal(ctx, actor, a.ID, ids[0]); !errors.Is(err, ErrForbidden) {
		t.Fatalf("pending original %v", err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.documents SET clearance='cleared' WHERE id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	// Expiry makes delivery unavailable, and cleanup removes object plus metadata.
	if _, err = owner.Exec(`UPDATE invoyis.archive_exports SET expires_at=now()-interval '1 second' WHERE id=$1`, receipt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DownloadExport(ctx, actor, a.ID, receipt.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired %v", err)
	}
	if err = s.CleanupExports(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = owner.QueryRow(`SELECT count(*) FROM invoyis.archive_exports WHERE id=$1`, receipt.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cleanup %d %v", count, err)
	}
	// Missing and mismatched originals fail before a bundle becomes deliverable.
	var key string
	if err = owner.QueryRow(`SELECT o.storage_key FROM invoyis.documents d JOIN invoyis.submissions sub ON sub.id=d.submission_id JOIN invoyis.stored_objects o ON o.id=sub.stored_object_id WHERE d.id=$1`, ids[0]).Scan(&key); err != nil {
		t.Fatal(err)
	}
	original := store.objects[key]
	delete(store.objects, key)
	if _, err = s.CreateExport(ctx, actor, a.ID); !errors.Is(err, ErrObjectMissing) {
		t.Fatalf("missing %v", err)
	}
	store.objects[key] = []byte("changed")
	if _, err = s.CreateExport(ctx, actor, a.ID); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("mismatch %v", err)
	}
	store.objects[key] = original
	other := addAccount(t, owner, "export-other@example.test")
	otherArchive, err := s.Create(ctx, other, "personal", "Other", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.DownloadOriginal(ctx, other, otherArchive.ID, ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross archive %v", err)
	}
	if _, err = s.DownloadExport(ctx, other, otherArchive.ID, second.ID); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross archive export %v", err)
	}
}
