package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
)

// clearedUpload uploads a synthetic PDF and marks it cleared and in review, as the
// worker does for any cleared file, with or without usable invoice candidates.
func clearedUpload(t *testing.T, s *Service, owner *sql.DB, store *fakeOriginals, actor int64, archiveID, label string) (docID string, data []byte, sha string) {
	t.Helper()
	data = []byte("%PDF-1.7\n% synthetic " + label + "\n%%EOF")
	sum := sha256.Sum256(data)
	sha = hex.EncodeToString(sum[:])
	session, err := s.CreateUpload(context.Background(), actor, archiveID, claim(data, label))
	if err != nil {
		t.Fatal(err)
	}
	store.objects[session.Grant.Fields["key"]] = data
	receipt, err := s.CompleteUpload(context.Background(), actor, archiveID, session.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.documents SET state='needs_review',clearance='cleared' WHERE id=$1`, receipt.DocumentID); err != nil {
		t.Fatal(err)
	}
	return receipt.DocumentID, data, sha
}

func write(t *testing.T, s *Service, actor int64, archiveID, docID, action string, version int64, m ReviewMetadata) (ReviewRead, error) {
	t.Helper()
	key, _ := newID()
	return s.WriteReview(context.Background(), actor, archiveID, docID, ReviewWrite{Action: action, ExpectedVersion: version, IdempotencyKey: key, Metadata: m, AcknowledgedWarnings: []string{}})
}

func TestNonInvoiceDocumentsFullWorkflow(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	archive, err := s.Create(ctx, actor, "personal", "Non-invoice archive", "")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		label string
		meta  ReviewMetadata
	}{
		{"lease", ReviewMetadata{DocumentType: TypeLease, Title: "Lease — Rothschild 1", Sender: "Dana Cohen", Reference: "000123", Notes: "Rothschild Blvd 1, תל אביב", IssueDate: "2026-01-01"}},
		{"court", ReviewMetadata{DocumentType: TypeCourtCorrespondence, Title: "מכתב מבית המשפט", Sender: "בית משפט השלום / Magistrate Court", Reference: "00417/2026-ת", Notes: "Hearing notice"}},
		{"other", ReviewMetadata{DocumentType: TypeOther, Title: "Warranty card"}},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			docID, data, sha := clearedUpload(t, s, owner, store, actor, archive.ID, c.label)
			c.meta.TaxLines = []TaxLine{}
			// Cleared PDF with no extraction run at all: review opens with no candidates.
			seed, err := s.ReadReview(ctx, actor, archive.ID, docID)
			if err != nil || seed.State != "needs_review" || seed.SourceRunID != nil {
				t.Fatalf("seed %+v %v", seed, err)
			}
			// Confirm with only title and type.
			minimal := ReviewMetadata{DocumentType: c.meta.DocumentType, Title: c.meta.Title, TaxLines: []TaxLine{}}
			if _, err = write(t, s, actor, archive.ID, docID, "confirm", 0, ReviewMetadata{DocumentType: c.meta.DocumentType, TaxLines: []TaxLine{}}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("confirmed without title: %v", err)
			}
			// Save optional metadata as an incomplete draft, reload it, then confirm.
			draft, err := write(t, s, actor, archive.ID, docID, "draft", 0, c.meta)
			if err != nil || draft.Draft == nil || draft.Draft.Metadata.Reference != c.meta.Reference {
				t.Fatalf("draft %+v %v", draft, err)
			}
			reloaded, err := s.ReadReview(ctx, actor, archive.ID, docID)
			if err != nil || reloaded.Draft == nil || reloaded.Draft.Metadata.Reference != c.meta.Reference || reloaded.Draft.Metadata.Notes != c.meta.Notes {
				t.Fatalf("reload %+v %v", reloaded, err)
			}
			done, err := write(t, s, actor, archive.ID, docID, "confirm", draft.ReviewVersion, c.meta)
			if err != nil || done.State != "ready" || done.Confirmed == nil {
				t.Fatalf("confirm %+v %v", done, err)
			}
			again, _ := s.ReadReview(ctx, actor, archive.ID, docID)
			if again.Confirmed.Metadata.Reference != c.meta.Reference || again.Confirmed.Metadata.Title != c.meta.Title {
				t.Fatalf("confirmed reload %+v", again.Confirmed)
			}
			raw, _ := json.Marshal(again.Confirmed.Metadata)
			for _, key := range []string{"supplier", "invoiceId", "currency", "total", "tax", "taxLines"} {
				var generic map[string]any
				_ = json.Unmarshal(raw, &generic)
				if _, present := generic[key]; present {
					t.Fatalf("confirmed %s revision carries %s", c.label, key)
				}
			}
			// The archive list shows the confirmed title and type.
			list, err := s.ListDocumentStatuses(ctx, actor, archive.ID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range list.Items {
				if item.ID == docID {
					found = true
					if item.Status != "ready" || item.ConfirmedType == nil || *item.ConfirmedType != c.meta.DocumentType || item.ConfirmedTitle == nil || *item.ConfirmedTitle != c.meta.Title {
						t.Fatalf("list item %+v", item)
					}
				}
			}
			if !found {
				t.Fatal("document missing from archive list")
			}
			// A later draft leaves the confirmed values intact.
			later := c.meta
			later.Title = "Draft only title"
			if _, err = write(t, s, actor, archive.ID, docID, "draft", done.ReviewVersion, later); err != nil {
				t.Fatal(err)
			}
			list, _ = s.ListDocumentStatuses(ctx, actor, archive.ID)
			for _, item := range list.Items {
				if item.ID == docID && *item.ConfirmedTitle != c.meta.Title {
					t.Fatalf("draft leaked into list: %+v", item)
				}
			}
			// Typed amount columns stay null and the stored original is untouched.
			var nonNull int
			if err = owner.QueryRow(`SELECT count(*) FROM invoyis.document_revisions WHERE document_id=$1 AND (subtotal IS NOT NULL OR stated_tax IS NOT NULL OR total IS NOT NULL)`, docID).Scan(&nonNull); err != nil || nonNull != 0 {
				t.Fatalf("amount columns %d %v", nonNull, err)
			}
			var storedSHA string
			var key string
			if err = owner.QueryRow(`SELECT o.sha256,o.storage_key FROM invoyis.documents d JOIN invoyis.submissions sub ON sub.id=d.submission_id JOIN invoyis.stored_objects o ON o.id=sub.stored_object_id WHERE d.id=$1`, docID).Scan(&storedSHA, &key); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(store.objects[key])
			if storedSHA != sha || hex.EncodeToString(sum[:]) != sha || string(store.objects[key]) != string(data) {
				t.Fatal("original bytes or checksum changed")
			}
			_ = minimal
		})
	}
	// Minimal confirmation: title and type only.
	docID, _, _ := clearedUpload(t, s, owner, store, actor, archive.ID, "minimal")
	done, err := write(t, s, actor, archive.ID, docID, "confirm", 0, ReviewMetadata{DocumentType: TypeOther, Title: "Only a title", TaxLines: []TaxLine{}})
	if err != nil || done.State != "ready" {
		t.Fatalf("minimal confirm %+v %v", done, err)
	}
}

func TestTypeSwitchFromInvoiceSuggestionPreservesHistory(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	archive, _ := s.Create(ctx, actor, "personal", "Switch", "")
	docID, _, sha := clearedUpload(t, s, owner, store, actor, archive.ID, "switch")
	run, _ := newID()
	candidates := `{"supplier":"Landlord Ltd","invoiceId":"00042","issueDate":"20261111","currency":"ILS","total":"10.00","tax":"0.00"}`
	if _, err := owner.Exec(`INSERT INTO invoyis.extraction_runs(id,archive_id,document_id,stage,input_sha256,extractor_version,candidates) VALUES($1,$2,$3,'document_xml',$4,'synthetic-test',$5)`, run, archive.ID, docID, sha, candidates); err != nil {
		t.Fatal(err)
	}
	invoice := ReviewMetadata{DocumentType: "invoice", Supplier: "Landlord Ltd", InvoiceID: "00042", IssueDate: "2026-11-11", Currency: "ILS", Total: amount("10.00"), Tax: amount("0.00"), TaxLines: []TaxLine{}}
	first, err := write(t, s, actor, archive.ID, docID, "draft", 0, invoice)
	if err != nil || first.Draft == nil {
		t.Fatal(err)
	}
	// A client that switches type but still submits invoice values is rejected server-side.
	conflicting := ReviewMetadata{DocumentType: TypeLease, Title: "Lease", Supplier: "Landlord Ltd", Total: amount("10.00"), TaxLines: []TaxLine{}}
	for _, action := range []string{"draft", "confirm"} {
		if _, err = write(t, s, actor, archive.ID, docID, action, first.ReviewVersion, conflicting); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s with conflicting invoice values: %v", action, err)
		}
	}
	// The same conflict arriving as raw API JSON.
	var body ReviewWrite
	payload := `{"action":"confirm","expectedVersion":1,"idempotencyKey":"6f1d0b0e-0f0a-4c55-8a58-1b2f3c4d5e6f","sourceRunId":null,"metadata":{"documentType":"court_correspondence","title":"T","tax":"0.00","taxLines":[]},"acknowledgedWarnings":[]}`
	if err = json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatal(err)
	}
	if _, err = s.WriteReview(ctx, actor, archive.ID, docID, body); !errors.Is(err, ErrInvalid) {
		t.Fatalf("raw API explicit zero tax on non-invoice: %v", err)
	}
	lease := ReviewMetadata{DocumentType: TypeLease, Title: "Lease", Sender: "Landlord Ltd", IssueDate: "2026-11-11", TaxLines: []TaxLine{}}
	key, _ := newID()
	confirmed, err := s.WriteReview(ctx, actor, archive.ID, docID, ReviewWrite{Action: "confirm", ExpectedVersion: first.ReviewVersion, IdempotencyKey: key, SourceRunID: &run, Metadata: lease, AcknowledgedWarnings: []string{}})
	if err != nil || confirmed.State != "ready" || len(confirmed.History) != 2 {
		t.Fatalf("switch %+v %v", confirmed, err)
	}
	// Prior revision unchanged; extraction output unchanged; candidates still readable.
	var prior []byte
	if err = owner.QueryRow(`SELECT metadata FROM invoyis.document_revisions WHERE id=$1`, first.Draft.ID).Scan(&prior); err != nil {
		t.Fatal(err)
	}
	var priorMeta ReviewMetadata
	_ = json.Unmarshal(prior, &priorMeta)
	if priorMeta.DocumentType != "invoice" || priorMeta.Supplier != "Landlord Ltd" || *priorMeta.Tax != "0.00" {
		t.Fatalf("prior revision changed: %s", prior)
	}
	var stored string
	if err = owner.QueryRow(`SELECT candidates::text FROM invoyis.extraction_runs WHERE id=$1`, run).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var want, got map[string]any
	_ = json.Unmarshal([]byte(candidates), &want)
	_ = json.Unmarshal([]byte(stored), &got)
	if len(want) != len(got) || got["supplier"] != "Landlord Ltd" || got["total"] != "10.00" {
		t.Fatalf("extraction output changed: %s", stored)
	}
	if confirmed.Candidates.Supplier != "Landlord Ltd" || confirmed.Confirmed.Metadata.Supplier != "" {
		t.Fatalf("candidates/confirmed %+v", confirmed)
	}
	// Database guard: a non-invoice revision cannot carry typed amounts.
	revision, _ := newID()
	_, err = owner.Exec(`INSERT INTO invoyis.document_revisions(id,archive_id,document_id,actor_id,kind,metadata,provenance,total) VALUES($1,$2,$3,$4,'draft','{"documentType":"lease","title":"x"}','{}',5)`, revision, archive.ID, docID, actor)
	if err == nil {
		t.Fatal("database accepted lease revision with a total")
	}
	_, err = owner.Exec(`INSERT INTO invoyis.document_revisions(id,archive_id,document_id,actor_id,kind,metadata,provenance) VALUES($1,$2,$3,$4,'draft','{"documentType":"other","title":"x","supplier":"s"}','{}')`, revision, archive.ID, docID, actor)
	if err == nil {
		t.Fatal("database accepted other revision with an invoice key")
	}
	// Switching back to invoice later is another append-only revision.
	back, err := write(t, s, actor, archive.ID, docID, "confirm", confirmed.ReviewVersion, invoice)
	if err != nil || len(back.History) != 3 || back.Confirmed.Metadata.DocumentType != "invoice" {
		t.Fatalf("switch back %+v %v", back, err)
	}
}

func TestNonInvoicePermissionsClearanceAndIsolation(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	archive, _ := s.Create(ctx, actor, "personal", "Permissions", "")
	docID, _, _ := clearedUpload(t, s, owner, store, actor, archive.ID, "permissions")
	meta := ReviewMetadata{DocumentType: TypeLease, Title: "Lease", TaxLines: []TaxLine{}}
	viewer := addAccount(t, owner, "noninvoice-viewer@example.test")
	if _, err := owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, archive.ID, viewer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadReview(ctx, viewer, archive.ID, docID); err != nil {
		t.Fatalf("viewer read: %v", err)
	}
	for _, action := range []string{"draft", "confirm"} {
		if _, err := write(t, s, viewer, archive.ID, docID, action, 0, meta); !errors.Is(err, ErrForbidden) {
			t.Fatalf("viewer %s: %v", action, err)
		}
	}
	// Another archive's owner cannot read or write this document, even by id.
	stranger := addAccount(t, owner, "noninvoice-stranger@example.test")
	other, err := s.Create(ctx, stranger, "personal", "Other archive", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadReview(ctx, stranger, archive.ID, docID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-archive read via victim archive: %v", err)
	}
	if _, err = s.ReadReview(ctx, stranger, other.ID, docID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-archive read via own archive: %v", err)
	}
	if _, err = write(t, s, stranger, other.ID, docID, "confirm", 0, meta); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-archive write: %v", err)
	}
	list, _ := s.ListDocumentStatuses(ctx, stranger, other.ID)
	if len(list.Items) != 0 {
		t.Fatalf("cross-archive list leak: %+v", list)
	}
	// Pending and blocked documents cannot be reviewed, confirmed, or reveal metadata.
	for _, clearance := range []string{"pending", "blocked"} {
		if _, err = owner.Exec(`UPDATE invoyis.documents SET clearance=$1 WHERE id=$2`, clearance, docID); err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"draft", "confirm"} {
			if _, err = write(t, s, actor, archive.ID, docID, action, 0, meta); !errors.Is(err, ErrForbidden) {
				t.Fatalf("%s %s: %v", clearance, action, err)
			}
		}
		if _, err = s.ReadReview(ctx, actor, archive.ID, docID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s read: %v", clearance, err)
		}
	}
	// Confirm once cleared, then lose clearance: list hides the confirmed metadata.
	if _, err = owner.Exec(`UPDATE invoyis.documents SET clearance='cleared' WHERE id=$1`, docID); err != nil {
		t.Fatal(err)
	}
	if _, err = write(t, s, actor, archive.ID, docID, "confirm", 0, meta); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.documents SET clearance='blocked' WHERE id=$1`, docID); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListDocumentStatuses(ctx, actor, archive.ID)
	for _, item := range list.Items {
		if item.ID == docID && (item.ConfirmedTitle != nil || item.ConfirmedType != nil) {
			t.Fatalf("blocked document exposes metadata: %+v", item)
		}
	}
}
