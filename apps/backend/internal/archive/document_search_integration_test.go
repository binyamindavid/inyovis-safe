package archive

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestBrowseSearchDatesAmountsAndArchiveScope(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	a, err := s.Create(ctx, actor, "personal", "Search A", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, actor, "personal", "Search B", "")
	if err != nil {
		t.Fatal(err)
	}
	supplier, _, err := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Café דוד")
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := s.CreateOrganisation(ctx, actor, b.ID, "suppliers", "Café דוד")
	if err != nil {
		t.Fatal(err)
	}
	category, _, err := s.CreateOrganisation(ctx, actor, a.ID, "categories", "Bills")
	if err != nil {
		t.Fatal(err)
	}
	created := 0
	create := func(archive string, m ReviewMetadata, f ReviewFiling) string {
		t.Helper()
		created++
		id, _, _ := clearedUpload(t, s, owner, store, actor, archive, fmt.Sprintf("search-fixture-%d", created))
		if _, err := reviewFilingWrite(t, s, actor, archive, id, "confirm", 0, m, f); err != nil {
			t.Fatal(err)
		}
		return id
	}
	invoice := func(currency, total, date, number string) ReviewMetadata {
		return ReviewMetadata{DocumentType: "invoice", Supplier: "Imprimé, ספק", InvoiceID: number, AccountReference: "00 1-A", IssueDate: date, Currency: currency, Total: amount(total), TaxLines: []TaxLine{}}
	}
	filing := ReviewFiling{SupplierID: filingID(supplier.ID), CategoryID: filingID(category.ID)}
	eur := create(a.ID, invoice("EUR", "5.00", "2026-01-01", "000-ABC"), filing)
	ils := create(a.ID, invoice("ILS", "5.00", "2026-01-02", "000-DEF"), filing)
	zero := create(a.ID, invoice("EUR", "0.00", "2026-01-03", "000-ZERO"), filing)
	lease := create(a.ID, ReviewMetadata{DocumentType: TypeLease, Title: "Bail Étage", Reference: "000-42", IssueDate: "2026-01-04"}, ReviewFiling{CategoryID: filingID(category.ID)})
	undated := create(a.ID, ReviewMetadata{DocumentType: TypeOther, Title: "Undated"}, ReviewFiling{CategoryID: filingID(category.ID)})
	foreignID := create(b.ID, invoice("EUR", "5.00", "2026-01-01", "000-ABC"), ReviewFiling{SupplierID: filingID(foreign.ID)})
	expect := func(f DocumentFilters, ids ...string) {
		t.Helper()
		page, err := s.BrowseDocuments(ctx, actor, a.ID, "", f, "")
		if err != nil || len(page.Items) != len(ids) {
			t.Fatalf("filters %+v: %+v %v", f, page, err)
		}
		got := map[string]bool{}
		for _, row := range page.Items {
			got[row.ID] = true
		}
		for _, id := range ids {
			if !got[id] {
				t.Fatalf("filters %+v missing %s: %+v", f, id, page)
			}
		}
	}
	expect(DocumentFilters{Query: "imprime, ספק"}, eur, ils, zero)
	expect(DocumentFilters{Query: "cafe דוד"}, eur, ils, zero)
	expect(DocumentFilters{Query: "000-ABC"}, eur)
	expect(DocumentFilters{Query: "000-42"}, lease)
	expect(DocumentFilters{Query: "etage"}, lease)
	expect(DocumentFilters{AccountReference: "00 1-A"}, eur, ils, zero)
	expect(DocumentFilters{AccountReference: "001-A"})
	expect(DocumentFilters{IssueDate: "2026-01-01"}, eur)
	expect(DocumentFilters{DateFrom: "2026-01-01", DateTo: "2026-01-02"}, eur, ils)
	expect(DocumentFilters{DateFrom: "2026-01-04", DateTo: "2026-01-04"}, lease)
	expect(DocumentFilters{DateFrom: "2026-01-01"}, eur, ils, zero, lease)
	expect(DocumentFilters{Total: "5.00", Currency: "EUR"}, eur)
	expect(DocumentFilters{Total: "5.00", Currency: "ILS"}, ils)
	expect(DocumentFilters{Total: "0", Currency: "EUR"}, zero)
	expect(DocumentFilters{CategoryID: category.ID, AccountReference: "00 1-A", DateFrom: "2026-01-01", DateTo: "2026-01-03", Total: "5.00", Currency: "EUR"}, eur)
	page, err := s.BrowseDocuments(ctx, actor, a.ID, "", DocumentFilters{}, "")
	if err != nil || len(page.Items) != 5 || page.Items[4].ID != undated || page.Items[4].IssueDate != nil {
		t.Fatalf("null date order: %+v %v", page, err)
	}
	if page.Items[0].DocumentNumber == nil || *page.Items[0].DocumentNumber != "000-42" {
		t.Fatalf("printed reference changed: %+v", page.Items[0])
	}
	if page.Items[1].PrintedSupplier == nil || *page.Items[1].PrintedSupplier != "Imprimé, ספק" {
		t.Fatalf("printed supplier changed: %+v", page.Items[1])
	}
	counts, err := s.CountDocuments(ctx, actor, a.ID)
	if err != nil || counts.Total != 5 {
		t.Fatalf("counts %+v %v", counts, err)
	}
	foreignPage, err := s.BrowseDocuments(ctx, actor, b.ID, "", DocumentFilters{Query: "cafe דוד"}, "")
	if err != nil || len(foreignPage.Items) != 1 || foreignPage.Items[0].ID != foreignID || foreignPage.Items[0].SupplierID == nil || *foreignPage.Items[0].SupplierID != foreign.ID {
		t.Fatalf("foreign archive: %+v %v", foreignPage, err)
	}
	if _, err = s.BrowseDocuments(ctx, actor, b.ID, "", DocumentFilters{}, encodeBrowseCursor(browseCursor{Archive: a.ID, Filters: DocumentFilters{}, ID: eur})); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross archive cursor: %v", err)
	}
}
