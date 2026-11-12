package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"testing"
)

func exportIndexRows(t *testing.T, s *Service, actor int64, archiveID string) [][]string {
	t.Helper()
	receipt, err := s.CreateExport(context.Background(), actor, archiveID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.DownloadExport(context.Background(), actor, archiveID, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.File {
		if f.Name == "index.csv" {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			b, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			return rows
		}
	}
	t.Fatal("missing index.csv")
	return nil
}

func TestExportConfirmedFilingAndArchiveIsolation(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage, s.Bundles = store, &fakeBundles{objects: map[string][]byte{}}
	a, err := s.Create(ctx, actor, "personal", "Export A", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, actor, "personal", "Export B", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a.ID, b.ID} {
		if _, err = owner.Exec(`UPDATE invoyis.archive_memberships SET can_bulk_export=true WHERE archive_id=$1 AND account_id=$2`, id, actor); err != nil {
			t.Fatal(err)
		}
	}
	supplier, _, err := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Café, חשמל")
	if err != nil {
		t.Fatal(err)
	}
	category, _, err := s.CreateOrganisation(ctx, actor, a.ID, "categories", "=Énergie")
	if err != nil {
		t.Fatal(err)
	}
	property, _, err := s.CreateOrganisation(ctx, actor, a.ID, "properties", "דירה \"A\"")
	if err != nil {
		t.Fatal(err)
	}
	paper, _, err := s.CreateOrganisation(ctx, actor, a.ID, "paper-locations", "ארון, A")
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := s.CreateOrganisation(ctx, actor, b.ID, "suppliers", "Café, חשמל")
	if err != nil {
		t.Fatal(err)
	}
	invoice, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "filed-invoice")
	printed := ReviewMetadata{DocumentType: "invoice", Supplier: "Printed, ספק", InvoiceID: "0007", AccountReference: "0001-AB 02", IssueDate: "2026-11-12", Currency: "ILS", Total: amount("10.00"), TaxLines: []TaxLine{}}
	filing := ReviewFiling{SupplierID: filingID(supplier.ID), CategoryID: filingID(category.ID), PropertyID: filingID(property.ID), PaperLocationID: filingID(paper.ID)}
	if _, err = reviewFilingWrite(t, s, actor, a.ID, invoice, "confirm", 0, printed, filing); err != nil {
		t.Fatal(err)
	}
	legacy, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "legacy")
	if _, err = write(t, s, actor, a.ID, legacy, "confirm", 0, ReviewMetadata{DocumentType: TypeLease, Title: "Legacy", TaxLines: []TaxLine{}}); err != nil {
		t.Fatal(err)
	}
	scan, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "scan")
	if _, err = reviewFilingWrite(t, s, actor, a.ID, scan, "confirm", 0, ReviewMetadata{DocumentType: TypeOther, Title: "Scan", TaxLines: []TaxLine{}}, ReviewFiling{CategoryID: filingID(category.ID), PropertyID: filingID(property.ID), ReferenceScan: true}); err != nil {
		t.Fatal(err)
	}
	other, _, _ := clearedUpload(t, s, owner, store, actor, b.ID, "other")
	if _, err = write(t, s, actor, b.ID, other, "confirm", 0, ReviewMetadata{DocumentType: TypeOther, Title: "Other archive", TaxLines: []TaxLine{}}); err != nil {
		t.Fatal(err)
	}
	foreignInvoice, _, _ := clearedUpload(t, s, owner, store, actor, b.ID, "foreign-invoice")
	if _, err = reviewFilingWrite(t, s, actor, b.ID, foreignInvoice, "confirm", 0, printed, ReviewFiling{SupplierID: filingID(foreign.ID)}); err != nil {
		t.Fatal(err)
	}
	rows := exportIndexRows(t, s, actor, a.ID)
	if len(rows) != 4 {
		t.Fatalf("archive A rows: %v", rows)
	}
	byID := map[string][]string{}
	for _, row := range rows[1:] {
		byID[row[1]] = row
	}
	r := byID[invoice]
	if r[8] != "Printed, ספק" || r[10] != "0001-AB 02" || r[15] != supplier.ID || r[16] != "Café, חשמל" || r[17] != category.ID || r[18] != "'=Énergie" || r[19] != property.ID || r[20] != "דירה \"A\"" || r[21] != paper.ID || r[22] != "ארון, A" {
		t.Fatalf("invoice row %v", r)
	}
	for _, v := range byID[legacy][15:] {
		if v != "" {
			t.Fatalf("legacy filing %v", byID[legacy])
		}
	}
	if byID[scan][17] != category.ID || byID[scan][19] != property.ID || byID[scan][21] != "" || byID[scan][23] != "true" {
		t.Fatalf("scan row %v", byID[scan])
	}
	if _, err = owner.Exec(`UPDATE invoyis.suppliers SET display_name='Renamed ספק' WHERE id=$1`, supplier.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.categories SET archived_at=now() WHERE id=$1`, category.ID); err != nil {
		t.Fatal(err)
	}
	draft := ReviewFiling{CategoryID: filingID(category.ID)}
	if _, err = reviewFilingWrite(t, s, actor, a.ID, invoice, "draft", 1, printed, draft); err != nil {
		t.Fatal(err)
	}
	rows = exportIndexRows(t, s, actor, a.ID)
	byID = map[string][]string{}
	for _, row := range rows[1:] {
		byID[row[1]] = row
	}
	if byID[invoice][15] != supplier.ID || byID[invoice][16] != "Renamed ספק" || byID[invoice][18] != "'=Énergie" {
		t.Fatalf("renamed/archived/draft row %v", byID[invoice])
	}
	if _, err = reviewFilingWrite(t, s, actor, a.ID, invoice, "confirm", 2, printed, draft); err != nil {
		t.Fatal(err)
	}
	rows = exportIndexRows(t, s, actor, a.ID)
	byID = map[string][]string{}
	for _, row := range rows[1:] {
		byID[row[1]] = row
	}
	if byID[invoice][15] != "" || byID[invoice][17] != category.ID || byID[invoice][18] != "'=Énergie" {
		t.Fatalf("confirmed draft row %v", byID[invoice])
	}
	otherRows := exportIndexRows(t, s, actor, b.ID)
	if len(otherRows) != 3 || otherRows[1][1] != other || otherRows[1][15] != "" || otherRows[2][1] != foreignInvoice || otherRows[2][15] != foreign.ID || otherRows[2][15] == supplier.ID || otherRows[2][16] != "Café, חשמל" {
		t.Fatalf("archive B rows %v", otherRows)
	}
}
