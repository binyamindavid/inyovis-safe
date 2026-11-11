package archive

import (
	"context"
	"errors"
	"testing"
)

func filingID(v string) *string { return &v }
func reviewFilingWrite(t *testing.T, s *Service, actor int64, archiveID, docID, action string, version int64, m ReviewMetadata, f ReviewFiling) (ReviewRead, error) {
	t.Helper()
	key, e := newID()
	if e != nil {
		t.Fatal(e)
	}
	return s.WriteReview(context.Background(), actor, archiveID, docID, ReviewWrite{Action: action, ExpectedVersion: version, IdempotencyKey: key, Metadata: m, Filing: &f, AcknowledgedWarnings: []string{}})
}
func TestReviewFilingDraftConfirmationAndIsolation(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	a, e := s.Create(ctx, actor, "personal", "Filing review", "")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Create(ctx, actor, "personal", "Other filing", "")
	if e != nil {
		t.Fatal(e)
	}
	supplier, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Café Électricité")
	if e != nil {
		t.Fatal(e)
	}
	category, _, e := s.CreateOrganisation(ctx, actor, a.ID, "categories", "Énergie")
	if e != nil {
		t.Fatal(e)
	}
	property, _, e := s.CreateOrganisation(ctx, actor, a.ID, "properties", "דירה א׳")
	if e != nil {
		t.Fatal(e)
	}
	paper, _, e := s.CreateOrganisation(ctx, actor, a.ID, "paper-locations", "ארון מסמכים")
	if e != nil {
		t.Fatal(e)
	}
	foreign, _, e := s.CreateOrganisation(ctx, actor, b.ID, "categories", "Foreign")
	if e != nil {
		t.Fatal(e)
	}
	contributor := addAccount(t, owner, "filing-contributor@example.test")
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'contributor')`, a.ID, contributor); e != nil {
		t.Fatal(e)
	}
	if _, created, e := s.CreateOrganisation(ctx, contributor, a.ID, "paper-locations", "Contributor shelf"); e != nil || !created {
		t.Fatalf("contributor filing create: %v %v", created, e)
	}
	if _, _, e := s.CreateOrganisation(ctx, contributor, a.ID, "suppliers", "Unauthorized supplier"); !errors.Is(e, ErrForbidden) {
		t.Fatalf("contributor supplier create: %v", e)
	}
	docID, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "filing-review")
	initial, e := s.ReadReview(ctx, actor, a.ID, docID)
	if e != nil || initial.Confirmed != nil || initial.Draft != nil {
		t.Fatalf("legacy empty review: %+v %v", initial, e)
	}
	printed := ReviewMetadata{DocumentType: "invoice", Supplier: "Café Electricité — חשמל", InvoiceID: "0007", IssueDate: "2026-11-11", Currency: "ILS", Total: amount("10.00"), TaxLines: []TaxLine{}}
	baselineKey, e := newID()
	if e != nil {
		t.Fatal(e)
	}
	baseline, e := s.WriteReview(ctx, actor, a.ID, docID, ReviewWrite{Action: "confirm", ExpectedVersion: 0, IdempotencyKey: baselineKey, Metadata: printed, AcknowledgedWarnings: []string{}})
	if e != nil || baseline.Confirmed == nil || baseline.Confirmed.Filing.SupplierID != nil || baseline.Confirmed.Filing.ReferenceScan {
		t.Fatalf("baseline: %+v %v", baseline, e)
	}
	filing := ReviewFiling{SupplierID: filingID(supplier.ID), CategoryID: filingID(category.ID), PropertyID: filingID(property.ID), PaperLocationID: filingID(paper.ID), ReferenceScan: true}
	draft, e := reviewFilingWrite(t, s, actor, a.ID, docID, "draft", 1, printed, filing)
	if e != nil || draft.Draft == nil {
		t.Fatalf("draft: %+v %v", draft, e)
	}
	reloaded, e := s.ReadReview(ctx, actor, a.ID, docID)
	if e != nil || reloaded.Draft == nil || reloaded.Confirmed == nil {
		t.Fatalf("reload: %+v %v", reloaded, e)
	}
	if reloaded.Draft.Filing.SupplierID == nil || *reloaded.Draft.Filing.SupplierID != supplier.ID || !reloaded.Draft.Filing.ReferenceScan || reloaded.Draft.Filing.PaperLocationID == nil || *reloaded.Draft.Filing.PaperLocationID != paper.ID {
		t.Fatalf("filing draft lost: %+v", reloaded.Draft.Filing)
	}
	if reloaded.Confirmed.Filing.SupplierID != nil || reloaded.Confirmed.Filing.ReferenceScan || reloaded.Confirmed.ID != baseline.Confirmed.ID {
		t.Fatalf("draft changed confirmed: %+v", reloaded.Confirmed)
	}
	var pointed string
	if e = owner.QueryRow(`SELECT current_confirmed_revision_id FROM invoyis.documents WHERE id=$1`, docID).Scan(&pointed); e != nil || pointed != baseline.Confirmed.ID {
		t.Fatalf("confirmed pointer before confirm: %s %v", pointed, e)
	}
	done, e := reviewFilingWrite(t, s, actor, a.ID, docID, "confirm", 2, printed, filing)
	if e != nil || done.Confirmed == nil || len(done.History) != 3 {
		t.Fatalf("confirm: %+v %v", done, e)
	}
	if done.Confirmed.Metadata.Supplier != printed.Supplier || done.Confirmed.Filing.SupplierID == nil || *done.Confirmed.Filing.SupplierID != supplier.ID || !done.Confirmed.Filing.ReferenceScan {
		t.Fatalf("printed/link separation: %+v", done.Confirmed)
	}
	if e = owner.QueryRow(`SELECT current_confirmed_revision_id FROM invoyis.documents WHERE id=$1`, docID).Scan(&pointed); e != nil || pointed != done.Confirmed.ID {
		t.Fatalf("confirmed pointer after confirm: %s %v", pointed, e)
	}
	history, e := s.ReadReview(ctx, actor, a.ID, docID)
	if e != nil || len(history.History) != 3 || history.Confirmed.Filing.CategoryID == nil || *history.Confirmed.Filing.CategoryID != category.ID {
		t.Fatalf("history: %+v %v", history, e)
	}
	later := filing
	laterDraft, e := reviewFilingWrite(t, s, actor, a.ID, docID, "draft", 3, printed, later)
	if e != nil || laterDraft.Confirmed.ID != done.Confirmed.ID || !laterDraft.Confirmed.Filing.ReferenceScan {
		t.Fatalf("later draft changed confirmed: %+v %v", laterDraft, e)
	}
	corrected := printed
	corrected.IssueDate = "2026-11-12"
	metadataOnlyKey, e := newID()
	if e != nil {
		t.Fatal(e)
	}
	metadataOnly, e := s.WriteReview(ctx, actor, a.ID, docID, ReviewWrite{Action: "draft", ExpectedVersion: 4, IdempotencyKey: metadataOnlyKey, Metadata: corrected, AcknowledgedWarnings: []string{}})
	if e != nil || metadataOnly.Draft == nil || metadataOnly.Draft.Filing.SupplierID == nil || *metadataOnly.Draft.Filing.SupplierID != supplier.ID || metadataOnly.Draft.Filing.CategoryID == nil || *metadataOnly.Draft.Filing.CategoryID != category.ID || metadataOnly.Draft.Filing.PropertyID == nil || *metadataOnly.Draft.Filing.PropertyID != property.ID || metadataOnly.Draft.Filing.PaperLocationID == nil || *metadataOnly.Draft.Filing.PaperLocationID != paper.ID || !metadataOnly.Draft.Filing.ReferenceScan {
		t.Fatalf("omitted filing did not inherit latest draft: %+v %v", metadataOnly.Draft, e)
	}
	cleared, e := reviewFilingWrite(t, s, actor, a.ID, docID, "draft", 5, corrected, ReviewFiling{})
	if e != nil || cleared.Draft == nil || cleared.Draft.Filing.SupplierID != nil || cleared.Draft.Filing.CategoryID != nil || cleared.Draft.Filing.PropertyID != nil || cleared.Draft.Filing.PaperLocationID != nil || cleared.Draft.Filing.ReferenceScan {
		t.Fatalf("explicit empty filing did not clear links: %+v %v", cleared.Draft, e)
	}
	if got, e := s.ReadReview(ctx, actor, a.ID, docID); e != nil || got.Draft == nil || got.Draft.Filing.SupplierID != nil || got.Confirmed.Filing.SupplierID == nil || *got.Confirmed.Filing.SupplierID != supplier.ID {
		t.Fatalf("cleared draft or confirmed history changed: %+v %v", got, e)
	}
	bad := filing
	bad.CategoryID = filingID(foreign.ID)
	if _, e = reviewFilingWrite(t, s, actor, a.ID, docID, "draft", 6, printed, bad); !errors.Is(e, ErrInvalid) {
		t.Fatalf("foreign category accepted: %v", e)
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a.ID, addAccount(t, owner, "filing-viewer@example.test")); e != nil {
		t.Fatal(e)
	}
	var viewer int64
	if e = owner.QueryRow(`SELECT id FROM invoyis.accounts WHERE email='filing-viewer@example.test'`).Scan(&viewer); e != nil {
		t.Fatal(e)
	}
	if _, e = reviewFilingWrite(t, s, viewer, a.ID, docID, "draft", 6, printed, filing); !errors.Is(e, ErrForbidden) {
		t.Fatalf("viewer write: %v", e)
	}
	if got, e := s.ReadReview(ctx, viewer, a.ID, docID); e != nil || got.Confirmed == nil {
		t.Fatalf("viewer historical read: %v", e)
	}
	// The composite database FK also rejects a direct SQL cross-archive reference.
	if _, e = owner.Exec(`UPDATE invoyis.document_revisions SET category_id=$1 WHERE id=$2`, foreign.ID, done.Confirmed.ID); e == nil {
		t.Fatal("direct SQL cross-archive link accepted")
	}
	if _, e = owner.Exec(`UPDATE invoyis.suppliers SET archived_at=now() WHERE id=$1`, supplier.ID); e != nil {
		t.Fatal(e)
	}
	if got, e := s.ReadReview(ctx, actor, a.ID, docID); e != nil || got.Confirmed.Filing.SupplierID == nil {
		t.Fatalf("archived historical supplier lost: %v", e)
	}
	if _, e = reviewFilingWrite(t, s, actor, a.ID, docID, "draft", 6, printed, filing); !errors.Is(e, ErrInvalid) {
		t.Fatalf("archived new selection accepted: %v", e)
	}
}

func TestReviewFilingNonInvoiceAndUnknownLocation(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	a, e := s.Create(ctx, actor, "personal", "Paper", "")
	if e != nil {
		t.Fatal(e)
	}
	category, _, e := s.CreateOrganisation(ctx, actor, a.ID, "categories", "Bail")
	if e != nil {
		t.Fatal(e)
	}
	docID, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "lease-scan")
	meta := ReviewMetadata{DocumentType: TypeLease, Title: "חוזה שכירות", TaxLines: []TaxLine{}}
	filing := ReviewFiling{CategoryID: filingID(category.ID), ReferenceScan: true}
	done, e := reviewFilingWrite(t, s, actor, a.ID, docID, "confirm", 0, meta, filing)
	if e != nil || done.Confirmed == nil || done.Confirmed.Filing.PaperLocationID != nil || !done.Confirmed.Filing.ReferenceScan {
		t.Fatalf("unknown paper location: %+v %v", done, e)
	}
	supplier, _, e := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "לא מתאים")
	if e != nil {
		t.Fatal(e)
	}
	filing.SupplierID = filingID(supplier.ID)
	if _, e = reviewFilingWrite(t, s, actor, a.ID, docID, "draft", 1, meta, filing); !errors.Is(e, ErrInvalid) {
		t.Fatalf("non-invoice supplier accepted: %v", e)
	}
}
