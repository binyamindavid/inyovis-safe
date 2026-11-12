package archive

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestBrowseConfirmedFilingPaginationAndIsolation(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	a, e := s.Create(ctx, actor, "personal", "Browse A", "")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Create(ctx, actor, "personal", "Browse B", "")
	if e != nil {
		t.Fatal(e)
	}
	supplier, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "ספק Café")
	if e != nil {
		t.Fatal(e)
	}
	category, _, e := s.CreateOrganisation(ctx, actor, a.ID, "categories", "Utilities")
	if e != nil {
		t.Fatal(e)
	}
	property, _, e := s.CreateOrganisation(ctx, actor, a.ID, "properties", "Flat 01")
	if e != nil {
		t.Fatal(e)
	}
	property2, _, e := s.CreateOrganisation(ctx, actor, a.ID, "properties", "Flat 02")
	if e != nil {
		t.Fatal(e)
	}
	paper, _, e := s.CreateOrganisation(ctx, actor, a.ID, "paper-locations", "Shelf A")
	if e != nil {
		t.Fatal(e)
	}
	foreign, _, e := s.CreateOrganisation(ctx, actor, b.ID, "categories", "Other")
	if e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	dates := []string{"2026-01-02", "2026-01-02", "2026-01-02", "2026-01-01", ""}
	for i, date := range dates {
		id, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, fmt.Sprintf("browse-%d", i))
		ids = append(ids, id)
		m := ReviewMetadata{DocumentType: "invoice", Supplier: "ספק Café", InvoiceID: "000-0", AccountReference: "00 1-A", IssueDate: date, Currency: "ILS", Total: amount("10.00")}
		f := ReviewFiling{SupplierID: filingID(supplier.ID), CategoryID: filingID(category.ID), PropertyID: filingID(property.ID), PaperLocationID: filingID(paper.ID)}
		if i == 4 {
			m = ReviewMetadata{DocumentType: "other", Title: "Undated"}
			f.SupplierID = nil
		}
		_, e = reviewFilingWrite(t, s, actor, a.ID, id, "confirm", 0, m, f)
		if e != nil {
			t.Fatal(e)
		}
	}
	// A supplier-free scan with unknown paper location remains filterable by category.
	scan, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "scan")
	_, e = reviewFilingWrite(t, s, actor, a.ID, scan, "confirm", 0, ReviewMetadata{DocumentType: "other", Title: "Reference", IssueDate: "2026-01-03"}, ReviewFiling{CategoryID: filingID(category.ID), ReferenceScan: true})
	if e != nil {
		t.Fatal(e)
	}
	all, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{}, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(all.Items) != 6 || all.Items[0].ID != scan || !all.Items[0].ReferenceScan || all.Items[0].PaperLocationName != nil {
		t.Fatalf("all: %+v", all)
	}
	filtered, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{CategoryID: category.ID}, "")
	if e != nil || len(filtered.Items) != 6 {
		t.Fatalf("category: %+v %v", filtered, e)
	}
	if _, e = s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{CategoryID: foreign.ID}, ""); e != nil {
		t.Fatal(e)
	}
	one, e := s.BrowseDocuments(ctx, actor, a.ID, supplier.ID, DocumentFilters{AccountReference: "00 1-A", PropertyID: property.ID}, "")
	if e != nil || len(one.Items) != 4 || one.Items[0].DocumentNumber == nil || *one.Items[0].DocumentNumber != "000-0" {
		t.Fatalf("history: %+v %v", one, e)
	}
	if all.Items[5].IssueDate != nil {
		t.Fatal("null date did not sort last")
	}
	wrong, e := s.BrowseDocuments(ctx, actor, a.ID, supplier.ID, DocumentFilters{AccountReference: "001-A"}, "")
	if e != nil || len(wrong.Items) != 0 {
		t.Fatalf("exact account: %+v %v", wrong, e)
	}
	// Draft filing changes do not move confirmed rows until confirmation.
	draft := ReviewFiling{SupplierID: filingID(supplier.ID), PropertyID: filingID(property2.ID)}
	m := ReviewMetadata{DocumentType: "invoice", Supplier: "New printed", InvoiceID: "000-0", AccountReference: "00 2-B", IssueDate: "2026-01-04", Currency: "ILS", Total: amount("12.00")}
	_, e = reviewFilingWrite(t, s, actor, a.ID, ids[0], "draft", 1, m, draft)
	if e != nil {
		t.Fatal(e)
	}
	before, e := s.BrowseDocuments(ctx, actor, a.ID, supplier.ID, DocumentFilters{PropertyID: property2.ID}, "")
	if e != nil || len(before.Items) != 0 {
		t.Fatalf("draft leaked: %+v %v", before, e)
	}
	_, e = reviewFilingWrite(t, s, actor, a.ID, ids[0], "confirm", 2, m, draft)
	if e != nil {
		t.Fatal(e)
	}
	after, e := s.BrowseDocuments(ctx, actor, a.ID, supplier.ID, DocumentFilters{PropertyID: property2.ID}, "")
	if e != nil || len(after.Items) != 1 || *after.Items[0].AccountReference != "00 2-B" {
		t.Fatalf("confirmation: %+v %v", after, e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.categories SET archived_at=now() WHERE id=$1`, category.ID); e != nil {
		t.Fatal(e)
	}
	archived, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{CategoryID: category.ID}, "")
	if e != nil || len(archived.Items) != 5 || archived.Items[0].CategoryName == nil {
		t.Fatalf("archived label: %+v %v", archived, e)
	}
	stranger := addAccount(t, owner, "browse-stranger@example.test")
	if _, e = s.BrowseDocuments(ctx, stranger, a.ID, "", DocumentFilters{CategoryID: category.ID}, ""); !errors.Is(e, ErrNotFound) {
		t.Fatalf("stranger: %v", e)
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a.ID, stranger); e != nil {
		t.Fatal(e)
	}
	allowed, e := s.BrowseDocuments(ctx, stranger, a.ID, "", DocumentFilters{}, "")
	if e != nil || len(allowed.Items) == 0 {
		t.Fatalf("viewer: %+v %v", allowed, e)
	}
	if _, e = owner.Exec(`DELETE FROM invoyis.archive_memberships WHERE archive_id=$1 AND account_id=$2`, a.ID, stranger); e != nil {
		t.Fatal(e)
	}
	if _, e = s.BrowseDocuments(ctx, stranger, a.ID, supplier.ID, DocumentFilters{PropertyID: property.ID}, ""); !errors.Is(e, ErrNotFound) {
		t.Fatalf("revoked supplier access: %v", e)
	}
	if _, e = s.BrowseDocuments(ctx, actor, b.ID, supplier.ID, DocumentFilters{}, ""); !errors.Is(e, ErrNotFound) {
		t.Fatalf("cross archive supplier: %v", e)
	}
}

func TestBrowseCursorBinding(t *testing.T) {
	f := DocumentFilters{PropertyID: "11111111-1111-1111-1111-111111111111"}
	a := "22222222-2222-2222-2222-222222222222"
	s := "33333333-3333-3333-3333-333333333333"
	d := "2026-01-01"
	raw := encodeBrowseCursor(browseCursor{Archive: a, Supplier: s, Filters: f, Date: &d, ID: "44444444-4444-4444-4444-444444444444"})
	if _, e := decodeBrowseCursor(raw, a, s, f); e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct {
		archive, supplier string
		filters           DocumentFilters
	}{{"55555555-5555-5555-5555-555555555555", s, f}, {a, "", f}, {a, s, DocumentFilters{}}} {
		if _, e := decodeBrowseCursor(raw, v.archive, v.supplier, v.filters); !errors.Is(e, ErrInvalid) {
			t.Fatalf("mismatched cursor: %v", e)
		}
	}
	if _, e := decodeBrowseCursor("bad!", a, s, f); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}

func TestBrowsePagesAcrossEqualAndNullDatesWithInsert(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	a, e := s.Create(ctx, actor, "personal", "Pages", "")
	if e != nil {
		t.Fatal(e)
	}
	create := func(i int, date string) {
		t.Helper()
		id, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, fmt.Sprintf("page-%d", i))
		m := ReviewMetadata{DocumentType: "other", Title: "Page", IssueDate: date}
		_, e := reviewFilingWrite(t, s, actor, a.ID, id, "confirm", 0, m, ReviewFiling{})
		if e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 19; i++ {
		create(i, "2026-02-01")
	}
	for i := 19; i < 23; i++ {
		create(i, "")
	}
	first, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{}, "")
	if e != nil || len(first.Items) != 20 || first.NextCursor == "" {
		t.Fatalf("first: %d %v", len(first.Items), e)
	}
	// A newer insert belongs before page one and must not reappear after its cursor.
	create(30, "2026-03-01")
	second, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{}, first.NextCursor)
	if e != nil || len(second.Items) != 3 || second.NextCursor != "" {
		t.Fatalf("second: %d %v", len(second.Items), e)
	}
	seen := map[string]bool{}
	for _, v := range append(first.Items, second.Items...) {
		if seen[v.ID] {
			t.Fatalf("duplicate %s", v.ID)
		}
		seen[v.ID] = true
	}
	if len(seen) != 23 {
		t.Fatalf("missing records: %d", len(seen))
	}
	if second.Items[0].IssueDate != nil {
		t.Fatal("null transition failed")
	}
	if _, e = s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{AccountReference: "changed"}, first.NextCursor); !errors.Is(e, ErrInvalid) {
		t.Fatalf("filter-bound cursor: %v", e)
	}
	viewer := addAccount(t, owner, "browse-pages-viewer@example.test")
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a.ID, viewer); e != nil {
		t.Fatal(e)
	}
	if _, e = s.BrowseDocuments(ctx, viewer, a.ID, "", DocumentFilters{}, first.NextCursor); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`DELETE FROM invoyis.archive_memberships WHERE archive_id=$1 AND account_id=$2`, a.ID, viewer); e != nil {
		t.Fatal(e)
	}
	if _, e = s.BrowseDocuments(ctx, viewer, a.ID, "", DocumentFilters{}, first.NextCursor); !errors.Is(e, ErrNotFound) {
		t.Fatalf("revoked cursor: %v", e)
	}
}

func TestBrowseSupplierHistoryUsesNonInvoiceReference(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	archive, e := s.Create(ctx, actor, "personal", "Reference history", "")
	if e != nil {
		t.Fatal(e)
	}
	supplier, _, e := s.CreateOrganisation(ctx, actor, archive.ID, "suppliers", "Landlord")
	if e != nil {
		t.Fatal(e)
	}
	id, _, _ := clearedUpload(t, s, owner, store, actor, archive.ID, "lease-reference")
	_, e = reviewFilingWrite(t, s, actor, archive.ID, id, "confirm", 0, ReviewMetadata{DocumentType: TypeLease, Title: "Lease", Reference: "000-42"}, ReviewFiling{})
	if e != nil {
		t.Fatal(e)
	}
	// Existing review policy does not create non-invoice supplier links. Seed a historical
	// confirmed link directly to verify the browser query preserves its printed reference.
	if _, e = owner.Exec(`UPDATE invoyis.document_revisions SET supplier_id=$1 WHERE archive_id=$2 AND document_id=$3 AND kind='confirmed'`, supplier.ID, archive.ID, id); e != nil {
		t.Fatal(e)
	}
	page, e := s.BrowseDocuments(ctx, actor, archive.ID, supplier.ID, DocumentFilters{}, "")
	if e != nil || len(page.Items) != 1 || page.Items[0].DocumentNumber == nil || *page.Items[0].DocumentNumber != "000-42" {
		t.Fatalf("lease reference: %+v %v", page, e)
	}
}

func TestBrowseStatusSearchAndArchiveCounts(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	a, e := s.Create(ctx, actor, "personal", "Status search", "")
	if e != nil {
		t.Fatal(e)
	}
	supplier, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Café Électricité")
	if e != nil {
		t.Fatal(e)
	}
	// Twenty-two dated, confirmed documents push an undated upload awaiting review beyond the first page of the unfiltered list.
	for i := 0; i < 22; i++ {
		id, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, fmt.Sprintf("dated-%d", i))
		m := ReviewMetadata{DocumentType: "other", Title: fmt.Sprintf("Lease %02d", i), IssueDate: "2026-02-01"}
		if _, e = reviewFilingWrite(t, s, actor, a.ID, id, "confirm", 0, m, ReviewFiling{}); e != nil {
			t.Fatal(e)
		}
	}
	bill, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "bill")
	bm := ReviewMetadata{DocumentType: "bill", Supplier: "Printed 50%", InvoiceID: "B-7", IssueDate: "2026-03-01", Currency: "ILS", Total: amount("5.00")}
	if _, e = reviewFilingWrite(t, s, actor, a.ID, bill, "confirm", 0, bm, ReviewFiling{SupplierID: filingID(supplier.ID)}); e != nil {
		t.Fatal(e)
	}
	waiting, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "waiting")

	first, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{}, "")
	if e != nil || len(first.Items) != 20 {
		t.Fatalf("first page: %d %v", len(first.Items), e)
	}
	for _, v := range first.Items {
		if v.ID == waiting {
			t.Fatal("undated upload unexpectedly on the first unfiltered page; the test no longer covers the paging gap")
		}
	}
	review, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{Status: "needs_review"}, "")
	if e != nil || len(review.Items) != 1 || review.Items[0].ID != waiting || review.Items[0].Status != "needs_review" {
		t.Fatalf("needs review: %+v %v", review, e)
	}
	attention, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{Status: "attention"}, "")
	if e != nil || len(attention.Items) != 1 || attention.Items[0].ID != waiting {
		t.Fatalf("attention: %+v %v", attention, e)
	}
	ready, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{Status: "ready"}, "")
	if e != nil || len(ready.Items) != 20 || ready.NextCursor == "" {
		t.Fatalf("ready page: %d %v", len(ready.Items), e)
	}
	readyNext, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{Status: "ready"}, ready.NextCursor)
	if e != nil || len(readyNext.Items) != 3 || readyNext.NextCursor != "" {
		t.Fatalf("ready next: %d %v", len(readyNext.Items), e)
	}
	if _, e = s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{}, ready.NextCursor); !errors.Is(e, ErrInvalid) {
		t.Fatalf("cursor crossed filters: %v", e)
	}
	datePage, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{IssueDate: "2026-02-01"}, "")
	if e != nil || len(datePage.Items) != 20 || datePage.NextCursor == "" {
		t.Fatalf("dated first page: %+v %v", datePage, e)
	}
	dateNext, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{IssueDate: "2026-02-01"}, datePage.NextCursor)
	if e != nil || len(dateNext.Items) != 2 {
		t.Fatalf("dated next page: %+v %v", dateNext, e)
	}
	if _, e = s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{DateFrom: "2026-02-01"}, datePage.NextCursor); !errors.Is(e, ErrInvalid) {
		t.Fatalf("date cursor crossed filters: %v", e)
	}
	for name, f := range map[string]DocumentFilters{
		"title":            {Query: "lease 07"},
		"literal wildcard": {Query: "50%"},
		"linked supplier":  {Query: "Électricité"},
		"invoice id":       {Query: "b-7"},
		"translated type":  {Query: "facture", Types: "bill"},
	} {
		got, e := s.BrowseDocuments(ctx, actor, a.ID, "", f, "")
		want := bill
		if name == "title" {
			want = ""
		}
		if e != nil || len(got.Items) != 1 || (want != "" && got.Items[0].ID != want) {
			t.Fatalf("%s: %+v %v", name, got, e)
		}
	}
	none, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{Query: "5_%"}, "")
	if e != nil || len(none.Items) != 0 {
		t.Fatalf("wildcards must be literal: %+v %v", none, e)
	}
	byName, e := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{Query: "חשבונית", Status: "needs_review"}, "")
	if e != nil || len(byName.Items) != 1 || byName.Items[0].ID != waiting {
		t.Fatalf("original filename: %+v %v", byName, e)
	}
	counts, e := s.CountDocuments(ctx, actor, a.ID)
	if e != nil || counts != (DocumentCounts{Total: 24, NeedsReview: 1, Attention: 1}) {
		t.Fatalf("counts: %+v %v", counts, e)
	}
	stranger := addAccount(t, owner, "counts-stranger@example.test")
	if _, e = s.CountDocuments(ctx, stranger, a.ID); !errors.Is(e, ErrNotFound) {
		t.Fatalf("stranger counts: %v", e)
	}
}
