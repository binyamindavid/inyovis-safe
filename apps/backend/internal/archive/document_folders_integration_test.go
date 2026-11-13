package archive

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"
)

// Folders group every listable document by linked supplier and by filing category (#59). Counts span all pages and
// statuses, a document appears in one company and one category folder, and missing metadata lands in "none".
func TestDocumentFoldersCountsMembershipAndFolderLists(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	a, e := s.Create(ctx, actor, "personal", "Folders A", "")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Create(ctx, actor, "personal", "Folders B", "")
	if e != nil {
		t.Fatal(e)
	}
	gas, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Israel Gas")
	if e != nil {
		t.Fatal(e)
	}
	water, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Water Co")
	if e != nil {
		t.Fatal(e)
	}
	utilities, _, e := s.CreateOrganisation(ctx, actor, a.ID, "categories", "Utilities")
	if e != nil {
		t.Fatal(e)
	}
	empty, _, e := s.CreateOrganisation(ctx, actor, a.ID, "categories", "Insurance")
	if e != nil {
		t.Fatal(e)
	}
	foreign, _, e := s.CreateOrganisation(ctx, actor, b.ID, "suppliers", "Foreign")
	if e != nil {
		t.Fatal(e)
	}
	// 22 Israel Gas invoices filed under Utilities span two list pages.
	for i := 0; i < 22; i++ {
		id, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, fmt.Sprintf("gas-%d", i))
		m := ReviewMetadata{DocumentType: "invoice", Supplier: "Israel Gas", InvoiceID: fmt.Sprintf("G-%02d", i), IssueDate: fmt.Sprintf("2026-02-%02d", i+1), Currency: "ILS", Total: amount("10.00")}
		if _, e = reviewFilingWrite(t, s, actor, a.ID, id, "confirm", 0, m, ReviewFiling{SupplierID: filingID(gas.ID), CategoryID: filingID(utilities.ID)}); e != nil {
			t.Fatal(e)
		}
	}
	// A Water Co bill with no category, a confirmed note with neither, and an upload still awaiting review.
	waterDoc, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "water")
	if _, e = reviewFilingWrite(t, s, actor, a.ID, waterDoc, "confirm", 0, ReviewMetadata{DocumentType: "bill", Supplier: "Water Co", InvoiceID: "W-1", IssueDate: "2026-03-01", Currency: "ILS", Total: amount("5.00")}, ReviewFiling{SupplierID: filingID(water.ID)}); e != nil {
		t.Fatal(e)
	}
	note, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "note")
	if _, e = reviewFilingWrite(t, s, actor, a.ID, note, "confirm", 0, ReviewMetadata{DocumentType: "other", Title: "Loose note"}, ReviewFiling{}); e != nil {
		t.Fatal(e)
	}
	pending, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "pending")

	companies, e := s.ListDocumentFolders(ctx, actor, a.ID, "company")
	if e != nil {
		t.Fatal(e)
	}
	counts := map[string]int64{}
	for _, f := range companies.Items {
		counts[f.DisplayName] = f.Count
	}
	if companies.Total != 25 || companies.Unassigned != 2 || counts["Israel Gas"] != 22 || counts["Water Co"] != 1 || len(companies.Items) != 2 {
		t.Fatalf("companies: %+v", companies)
	}
	categories, e := s.ListDocumentFolders(ctx, actor, a.ID, "category")
	if e != nil {
		t.Fatal(e)
	}
	// The empty active category is still listed; Uncategorised holds the bill, the note and the pending upload.
	if categories.Total != 25 || categories.Unassigned != 3 || len(categories.Items) != 2 || categories.Items[0].ID != empty.ID || categories.Items[0].Count != 0 || categories.Items[1].Count != 22 {
		t.Fatalf("categories: %+v", categories)
	}

	// The same invoice opens from its company folder and its category folder.
	first, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{SupplierID: gas.ID}, "")
	if e != nil || len(first.Items) != 20 || first.NextCursor == "" {
		t.Fatalf("company page 1: %d %q %v", len(first.Items), first.NextCursor, e)
	}
	second, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{SupplierID: gas.ID}, first.NextCursor)
	if e != nil || len(second.Items) != 2 || second.NextCursor != "" {
		t.Fatalf("company page 2: %+v %v", second, e)
	}
	byCategory, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{CategoryID: utilities.ID}, "")
	if e != nil || byCategory.Items[0].ID != first.Items[0].ID {
		t.Fatalf("same document in both folders: %+v %v", byCategory, e)
	}
	unassigned, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{SupplierID: folderNone}, "")
	if e != nil || len(unassigned.Items) != 2 {
		t.Fatalf("unassigned company: %+v %v", unassigned, e)
	}
	seen := map[string]bool{}
	for _, d := range unassigned.Items {
		seen[d.ID] = true
	}
	if !seen[note] || !seen[pending] {
		t.Fatalf("unassigned rows: %+v", unassigned)
	}
	uncategorised, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{CategoryID: folderNone, Status: "ready"}, "")
	if e != nil || len(uncategorised.Items) != 2 {
		t.Fatalf("uncategorised ready: %+v %v", uncategorised, e)
	}
	scoped, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{SupplierID: gas.ID, Query: "G-07"}, "")
	if e != nil || len(scoped.Items) != 1 {
		t.Fatalf("search within folder: %+v %v", scoped, e)
	}

	// Archived suppliers stay while they hold documents.
	if _, e = owner.Exec(`UPDATE invoyis.suppliers SET archived_at=now() WHERE id=$1`, water.ID); e != nil {
		t.Fatal(e)
	}
	companies, e = s.ListDocumentFolders(ctx, actor, a.ID, "company")
	if e != nil || len(companies.Items) != 2 || !companies.Items[1].Archived {
		t.Fatalf("archived supplier: %+v %v", companies, e)
	}

	// Another archive's supplier is not a folder here, and folders follow membership.
	if _, e = s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{SupplierID: foreign.ID}, ""); !errors.Is(e, ErrNotFound) {
		t.Fatalf("foreign supplier folder: %v", e)
	}
	other, e := s.ListDocumentFolders(ctx, actor, b.ID, "company")
	if e != nil || other.Total != 0 || len(other.Items) != 1 || other.Items[0].Count != 0 {
		t.Fatalf("archive B: %+v %v", other, e)
	}
	stranger := addAccount(t, owner, "folders-stranger@example.test")
	if _, e = s.ListDocumentFolders(ctx, stranger, a.ID, "company"); !errors.Is(e, ErrNotFound) {
		t.Fatalf("stranger folders: %v", e)
	}
	if _, e = s.BrowseDocuments(ctx, stranger, a.ID, "", DocumentFilters{SupplierID: gas.ID}, ""); !errors.Is(e, ErrNotFound) {
		t.Fatalf("stranger folder list: %v", e)
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a.ID, stranger); e != nil {
		t.Fatal(e)
	}
	viewer, e := s.ListDocumentFolders(ctx, stranger, a.ID, "company")
	if e != nil || viewer.Total != 25 {
		t.Fatalf("viewer folders: %+v %v", viewer, e)
	}
	if _, e = s.ListDocumentFolders(ctx, actor, a.ID, "kind"); !errors.Is(e, ErrInvalid) {
		t.Fatalf("unknown grouping: %v", e)
	}
}

func TestDocumentFolderFilterParsing(t *testing.T) {
	ok := []string{"supplierId=none", "categoryId=none", "supplierId=0b0b0b0b-0b0b-4b0b-8b0b-0b0b0b0b0b0b&q=gas"}
	for _, raw := range ok {
		q, _ := url.ParseQuery(raw)
		if _, _, e := parseDocumentFilters(q, false); e != nil {
			t.Fatalf("%s: %v", raw, e)
		}
	}
	bad := []string{"supplierId=None", "supplierId=0B0B0B0B-0B0B-4B0B-8B0B-0B0B0B0B0B0B", "categoryId=unassigned", "propertyId=none"}
	for _, raw := range bad {
		q, _ := url.ParseQuery(raw)
		if _, _, e := parseDocumentFilters(q, false); !errors.Is(e, ErrInvalid) {
			t.Fatalf("%s accepted", raw)
		}
	}
	q, _ := url.ParseQuery("supplierId=none")
	if _, _, e := parseDocumentFilters(q, true); !errors.Is(e, ErrInvalid) {
		t.Fatal("supplier history accepted a supplier folder")
	}
}
