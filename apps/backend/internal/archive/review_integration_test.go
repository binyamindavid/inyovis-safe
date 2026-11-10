package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"testing"
)

func amount(value string) *string { return &value }
func TestReviewRevisionAndClearance(t *testing.T) {
	service, _, owner, actor := setup(t)
	ctx := context.Background()
	storage := &fakeOriginals{objects: map[string][]byte{}}
	service.Storage = storage
	archive, e := service.Create(ctx, actor, "personal", "Synthetic review", "")
	if e != nil {
		t.Fatal(e)
	}
	data := []byte("%PDF-1.7\nsynthetic review\n%%EOF")
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	session, e := service.CreateUpload(ctx, actor, archive.ID, claim(data, "review"))
	if e != nil {
		t.Fatal(e)
	}
	storage.objects[session.Grant.Fields["key"]] = data
	receipt, e := service.CompleteUpload(ctx, actor, archive.ID, session.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.documents SET state='needs_review',clearance='cleared' WHERE id=$1`, receipt.DocumentID); e != nil {
		t.Fatal(e)
	}
	run, e := newID()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.extraction_runs(id,archive_id,document_id,stage,input_sha256,extractor_version,candidates) VALUES($1,$2,$3,'document_xml',$4,'synthetic-test','{"supplier":"Wrong","invoiceId":"00042","issueDate":"20261111","currency":"ILS","total":"10.00"}')`, run, archive.ID, receipt.DocumentID, hash); e != nil {
		t.Fatal(e)
	}
	review, e := service.ReadReview(ctx, actor, archive.ID, receipt.DocumentID)
	if e != nil {
		t.Fatal(e)
	}
	if review.SourceRunID == nil || *review.SourceRunID != run || review.Candidates.IssueDate != "2026-11-11" {
		t.Fatalf("seed %+v", review)
	}
	meta := ReviewMetadata{DocumentType: "invoice", Supplier: "Correct", InvoiceID: "00042", IssueDate: "2026-11-11", Currency: "ILS", Total: amount("10.00"), TaxLines: []TaxLine{}}
	key, _ := newID()
	draft := ReviewWrite{Action: "draft", ExpectedVersion: 0, IdempotencyKey: key, SourceRunID: &run, Metadata: meta, AcknowledgedWarnings: []string{}}
	saved, e := service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, draft)
	if e != nil {
		t.Fatal(e)
	}
	if saved.ReviewVersion != 1 || saved.Draft == nil || saved.State != "needs_review" {
		t.Fatalf("draft %+v", saved)
	}
	replay, e := service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, draft)
	if e != nil || replay.ReviewVersion != 1 || len(replay.History) != 1 {
		t.Fatalf("replay %+v %v", replay, e)
	}
	changed := draft
	changed.Metadata.Supplier = "Changed"
	if _, e = service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, changed); !errors.Is(e, ErrConflict) {
		t.Fatalf("changed key %v", e)
	}
	key, _ = newID()
	confirm := ReviewWrite{Action: "confirm", ExpectedVersion: 1, IdempotencyKey: key, SourceRunID: &run, Metadata: meta, AcknowledgedWarnings: []string{}}
	confirmed, e := service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, confirm)
	if e != nil {
		t.Fatal(e)
	}
	if confirmed.State != "ready" || confirmed.Confirmed == nil || len(confirmed.History) != 2 || confirmed.Confirmed.Provenance["supplier"] != "corrected" {
		t.Fatalf("confirmation %+v", confirmed)
	}
	if _, e = service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, confirm); e != nil {
		t.Fatalf("confirmation replay %v", e)
	}
	key, _ = newID()
	draft.IdempotencyKey = key
	draft.ExpectedVersion = 1
	if latest, e := service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, draft); !errors.Is(e, ErrConflict) || latest.ReviewVersion != 2 {
		t.Fatalf("stale %+v %v", latest, e)
	}
	var revisions, audits int
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.document_revisions WHERE document_id=$1`, receipt.DocumentID).Scan(&revisions); e != nil {
		t.Fatal(e)
	}
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.archive_audit WHERE action LIKE 'document_review_%' AND details->>'documentId'=$1`, receipt.DocumentID).Scan(&audits); e != nil {
		t.Fatal(e)
	}
	if revisions != 2 || audits != 2 {
		t.Fatalf("duplicate writes: %d revisions %d audits", revisions, audits)
	}
	viewer := addAccount(t, owner, "review-viewer@example.test")
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, archive.ID, viewer); e != nil {
		t.Fatal(e)
	}
	if _, e = service.ReadReview(ctx, viewer, archive.ID, receipt.DocumentID); e != nil {
		t.Fatal(e)
	}
	if _, e = service.WriteReview(ctx, viewer, archive.ID, receipt.DocumentID, confirm); !errors.Is(e, ErrForbidden) {
		t.Fatalf("viewer write %v", e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.documents SET clearance='blocked' WHERE id=$1`, receipt.DocumentID); e != nil {
		t.Fatal(e)
	}
	if _, e = service.ReadReview(ctx, actor, archive.ID, receipt.DocumentID); !errors.Is(e, ErrForbidden) {
		t.Fatalf("blocked read %v", e)
	}
	if _, e = service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, confirm); !errors.Is(e, ErrForbidden) {
		t.Fatalf("blocked replay %v", e)
	}
	var storedHash string
	if e = owner.QueryRow(`SELECT o.sha256 FROM invoyis.stored_objects o JOIN invoyis.submissions s ON s.stored_object_id=o.id JOIN invoyis.documents d ON d.submission_id=s.id WHERE d.id=$1`, receipt.DocumentID).Scan(&storedHash); e != nil || storedHash != hash {
		t.Fatalf("original %s %v", storedHash, e)
	}
}

func TestPreviewClaimSharedAcrossInstances(t *testing.T) {
	service, _, owner, actor := setup(t)
	ctx := context.Background()
	storage := &fakeOriginals{objects: map[string][]byte{}}
	service.Storage = storage
	archive, e := service.Create(ctx, actor, "personal", "Synthetic preview", "")
	if e != nil {
		t.Fatal(e)
	}
	data := []byte("%PDF-1.7\nsynthetic preview\n%%EOF")
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	session, e := service.CreateUpload(ctx, actor, archive.ID, claim(data, "preview"))
	if e != nil {
		t.Fatal(e)
	}
	storage.objects[session.Grant.Fields["key"]] = data
	receipt, e := service.CompleteUpload(ctx, actor, archive.ID, session.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.documents SET state='needs_review',clearance='cleared' WHERE id=$1`, receipt.DocumentID); e != nil {
		t.Fatal(e)
	}
	secondDB, e := sql.Open("pgx", os.Getenv("TEST_ARCHIVE_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	defer secondDB.Close()
	second := &Service{DB: secondDB, Storage: storage}
	firstOwner, _ := newID()
	secondOwner, _ := newID()
	won, e := service.claimPreview(ctx, actor, archive.ID, receipt.DocumentID, hash, 1, firstOwner, true)
	if e != nil || !won {
		t.Fatalf("first claim %v %v", won, e)
	}
	won, e = second.claimPreview(ctx, actor, archive.ID, receipt.DocumentID, hash, 1, secondOwner, true)
	if e != nil || won {
		t.Fatalf("second claim %v %v", won, e)
	}
	image := []byte("synthetic-derived-image")
	if e = service.finishPreview(ctx, actor, archive.ID, receipt.DocumentID, hash, 1, firstOwner, image, 1); e != nil {
		t.Fatal(e)
	}
	found, e := second.previewLookup(ctx, actor, archive.ID, receipt.DocumentID, 1)
	if e != nil || string(found.cached) != string(image) {
		t.Fatalf("shared cache %q %v", found.cached, e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.documents SET clearance='pending' WHERE id=$1`, receipt.DocumentID); e != nil {
		t.Fatal(e)
	}
	if _, e = second.previewLookup(ctx, actor, archive.ID, receipt.DocumentID, 1); !errors.Is(e, ErrForbidden) {
		t.Fatalf("pending cached preview %v", e)
	}
}
