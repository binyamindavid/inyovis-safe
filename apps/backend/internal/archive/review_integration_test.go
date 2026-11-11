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
func TestReviewManualEntryKeepsTaxNullDistinctFromZero(t *testing.T) {
	service, _, owner, actor := setup(t)
	ctx := context.Background()
	data := []byte("%PDF-1.7\nmanual review\n%%EOF")
	storage := &fakeOriginals{objects: map[string][]byte{}}
	service.Storage = storage
	archive, err := service.Create(ctx, actor, "personal", "Manual review", "")
	if err != nil {
		t.Fatal(err)
	}
	upload, err := service.CreateUpload(ctx, actor, archive.ID, claim(data, "manual"))
	if err != nil {
		t.Fatal(err)
	}
	storage.objects[upload.Grant.Fields["key"]] = data
	receipt, err := service.CompleteUpload(ctx, actor, archive.ID, upload.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.documents SET state='needs_review',clearance='cleared' WHERE id=$1`, receipt.DocumentID); err != nil {
		t.Fatal(err)
	}
	seed, err := service.ReadReview(ctx, actor, archive.ID, receipt.DocumentID)
	if err != nil || seed.SourceRunID != nil {
		t.Fatalf("manual seed %+v %v", seed, err)
	}
	meta := ReviewMetadata{DocumentType: "invoice", Supplier: "Manual", InvoiceID: "0007", IssueDate: "2026-11-11", Currency: "ILS", Total: amount("10.00"), TaxLines: []TaxLine{}}
	key, _ := newID()
	for _, state := range []string{"extracting", "failed"} {
		if _, err = owner.Exec(`UPDATE invoyis.documents SET state=$1 WHERE id=$2`, state, receipt.DocumentID); err != nil {
			t.Fatal(err)
		}
		if _, err = service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, ReviewWrite{Action: "confirm", ExpectedVersion: 0, IdempotencyKey: key, Metadata: meta}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("confirmed %s document: %v", state, err)
		}
	}
	if _, err = owner.Exec(`UPDATE invoyis.documents SET state='needs_review' WHERE id=$1`, receipt.DocumentID); err != nil {
		t.Fatal(err)
	}
	first, err := service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, ReviewWrite{Action: "confirm", ExpectedVersion: 0, IdempotencyKey: key, Metadata: meta})
	if err != nil || first.Confirmed == nil || first.Confirmed.Metadata.Tax != nil {
		t.Fatalf("unstated tax %+v %v", first, err)
	}
	meta.Tax = amount("0.00")
	key, _ = newID()
	second, err := service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, ReviewWrite{Action: "confirm", ExpectedVersion: 1, IdempotencyKey: key, Metadata: meta})
	if err != nil || second.Confirmed == nil || second.Confirmed.Metadata.Tax == nil || *second.Confirmed.Metadata.Tax != "0.00" || len(second.History) != 2 {
		t.Fatalf("zero tax %+v %v", second, err)
	}
	var nullCount, zeroCount int
	if err = owner.QueryRow(`SELECT count(*) FILTER (WHERE stated_tax IS NULL),count(*) FILTER (WHERE stated_tax=0) FROM invoyis.document_revisions WHERE document_id=$1`, receipt.DocumentID).Scan(&nullCount, &zeroCount); err != nil || nullCount != 1 || zeroCount != 1 {
		t.Fatalf("stored tax null=%d zero=%d: %v", nullCount, zeroCount, err)
	}
	meta.Subtotal = amount("9.00")
	key, _ = newID()
	warned := ReviewWrite{Action: "confirm", ExpectedVersion: 2, IdempotencyKey: key, Metadata: meta}
	if _, err = service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, warned); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unacknowledged mismatch %v", err)
	}
	warned.AcknowledgedWarnings = []string{"TOTAL_MISMATCH"}
	third, err := service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, warned)
	if err != nil || third.Confirmed == nil || len(third.Confirmed.AcknowledgedWarnings) != 1 || third.ReviewVersion != 3 {
		t.Fatalf("acknowledged mismatch %+v %v", third, err)
	}
	var workerExists, workerCanUpdate bool
	if err = owner.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='invoyis_document_worker')`).Scan(&workerExists); err != nil {
		t.Fatal(err)
	}
	if workerExists {
		if err = owner.QueryRow(`SELECT has_column_privilege('invoyis_document_worker','invoyis.documents','state','UPDATE')`).Scan(&workerCanUpdate); err != nil || workerCanUpdate {
			t.Fatalf("worker direct Ready privilege=%v %v", workerCanUpdate, err)
		}
	}
	lease, _ := newID()
	if _, err = owner.Exec(`UPDATE invoyis.processing_jobs SET state='running',lease_token=$1,lease_expires_at=now()+interval '1 minute' WHERE document_id=$2`, lease, receipt.DocumentID); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	var completed bool
	if err = owner.QueryRow(`SELECT invoyis.worker_complete(j.id,$1,$2,'reprocess-test','basic','{"supplier":"New extraction"}'::jsonb,'{}'::jsonb,'[]'::jsonb) FROM invoyis.processing_jobs j WHERE j.document_id=$3`, lease, hex.EncodeToString(digest[:]), receipt.DocumentID).Scan(&completed); err != nil || !completed {
		t.Fatalf("reprocessing complete=%v %v", completed, err)
	}
	after, err := service.ReadReview(ctx, actor, archive.ID, receipt.DocumentID)
	if err != nil || after.State != "ready" || after.ReviewVersion != 3 || after.Confirmed == nil || after.Confirmed.ID != third.Confirmed.ID || after.Confirmed.Metadata.Supplier != "Manual" || len(after.History) != 3 {
		t.Fatalf("reprocessing changed review %+v %v", after, err)
	}
}
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
	for _, statement := range []string{
		`UPDATE invoyis.document_revisions SET metadata='{}' WHERE id=$1`,
		`DELETE FROM invoyis.document_revisions WHERE id=$1`,
	} {
		tx, err := service.transaction(ctx, actor, archive.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.ExecContext(ctx, statement, confirmed.Confirmed.ID)
		_ = tx.Rollback()
		if err == nil {
			t.Fatalf("runtime mutated append-only revision with %s", statement)
		}
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
	// Two in-flight confirmations with the same key share one committed receipt.
	key, _ = newID()
	concurrent := ReviewWrite{Action: "confirm", ExpectedVersion: 2, IdempotencyKey: key, SourceRunID: &run, Metadata: meta, AcknowledgedWarnings: []string{}}
	concurrent.Metadata.Supplier = "Concurrent correction"
	type outcome struct {
		label string
		state ReviewRead
		err   error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			state, err := service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, concurrent)
			results <- outcome{state: state, err: err}
		}()
	}
	close(start)
	for range 2 {
		got := <-results
		if got.err != nil || got.state.ReviewVersion != 3 || len(got.state.History) != 3 {
			t.Fatalf("concurrent replay %+v %v", got.state, got.err)
		}
	}
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.document_revisions WHERE document_id=$1`, receipt.DocumentID).Scan(&revisions); e != nil {
		t.Fatal(e)
	}
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.archive_audit WHERE action LIKE 'document_review_%' AND details->>'documentId'=$1`, receipt.DocumentID).Scan(&audits); e != nil {
		t.Fatal(e)
	}
	if revisions != 3 || audits != 3 {
		t.Fatalf("duplicate confirm: %d revisions %d audits", revisions, audits)
	}

	// Independent sessions race on one review_version. The losing receipt carries the latest values.
	contributor := addAccount(t, owner, "review-contributor@example.test")
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'contributor')`, archive.ID, contributor); e != nil {
		t.Fatal(e)
	}
	secondDB, e := sql.Open("pgx", os.Getenv("TEST_ARCHIVE_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	defer secondDB.Close()
	second := &Service{DB: secondDB}
	if _, checkErr := second.ReadReview(ctx, contributor, archive.ID, receipt.DocumentID); checkErr != nil {
		t.Fatalf("contributor read: %v", checkErr)
	}
	draftKey, _ := newID()
	confirmKey, _ := newID()
	racingDraft := ReviewWrite{Action: "draft", ExpectedVersion: 3, IdempotencyKey: draftKey, SourceRunID: &run, Metadata: meta, AcknowledgedWarnings: []string{}}
	racingDraft.Metadata.Supplier = "Draft racing confirmation"
	racingConfirm := ReviewWrite{Action: "confirm", ExpectedVersion: 3, IdempotencyKey: confirmKey, SourceRunID: &run, Metadata: meta, AcknowledgedWarnings: []string{}}
	racingConfirm.Metadata.Supplier = "Confirmation racing draft"
	start = make(chan struct{})
	results = make(chan outcome, 2)
	go func() {
		<-start
		state, err := service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, racingDraft)
		results <- outcome{label: "draft", state: state, err: err}
	}()
	go func() {
		<-start
		state, err := second.WriteReview(ctx, contributor, archive.ID, receipt.DocumentID, racingConfirm)
		results <- outcome{label: "confirm", state: state, err: err}
	}()
	close(start)
	wins, stale := 0, 0
	for range 2 {
		got := <-results
		switch {
		case got.err == nil && got.state.ReviewVersion == 4:
			wins++
		case errors.Is(got.err, ErrConflict) && got.state.ReviewVersion == 4:
			stale++
		default:
			t.Fatalf("draft/confirm race %s %+v %v", got.label, got.state, got.err)
		}
	}
	if wins != 1 || stale != 1 {
		t.Fatalf("race wins=%d stale=%d", wins, stale)
	}
	// Two different reviewers confirming the same version cannot overwrite each other.
	firstKey, _ := newID()
	secondKey, _ := newID()
	firstConfirm := ReviewWrite{Action: "confirm", ExpectedVersion: 4, IdempotencyKey: firstKey, SourceRunID: &run, Metadata: meta}
	firstConfirm.Metadata.Supplier = "First reviewer"
	secondConfirm := ReviewWrite{Action: "confirm", ExpectedVersion: 4, IdempotencyKey: secondKey, SourceRunID: &run, Metadata: meta}
	secondConfirm.Metadata.Supplier = "Second reviewer"
	start = make(chan struct{})
	results = make(chan outcome, 2)
	go func() {
		<-start
		state, err := service.WriteReview(ctx, actor, archive.ID, receipt.DocumentID, firstConfirm)
		results <- outcome{label: "first confirm", state: state, err: err}
	}()
	go func() {
		<-start
		state, err := second.WriteReview(ctx, contributor, archive.ID, receipt.DocumentID, secondConfirm)
		results <- outcome{label: "second confirm", state: state, err: err}
	}()
	close(start)
	wins, stale = 0, 0
	for range 2 {
		got := <-results
		switch {
		case got.err == nil && got.state.ReviewVersion == 5:
			wins++
		case errors.Is(got.err, ErrConflict) && got.state.ReviewVersion == 5 && got.state.Confirmed != nil:
			stale++
		default:
			t.Fatalf("confirm race %s %+v %v", got.label, got.state, got.err)
		}
	}
	if wins != 1 || stale != 1 {
		t.Fatalf("confirm race wins=%d stale=%d", wins, stale)
	}
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.document_revisions WHERE document_id=$1`, receipt.DocumentID).Scan(&revisions); e != nil || revisions != 5 {
		t.Fatalf("confirm race revisions=%d %v", revisions, e)
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
	if _, e = owner.Exec(`UPDATE invoyis.documents SET clearance='blocked' WHERE id=$1`, receipt.DocumentID); e != nil {
		t.Fatal(e)
	}
	if _, e = second.previewLookup(ctx, actor, archive.ID, receipt.DocumentID, 1); !errors.Is(e, ErrForbidden) {
		t.Fatalf("blocked cached preview %v", e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.documents SET clearance='cleared' WHERE id=$1`, receipt.DocumentID); e != nil {
		t.Fatal(e)
	}
	other, e := service.Create(ctx, actor, "personal", "Other preview archive", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = second.previewLookup(ctx, actor, other.ID, receipt.DocumentID, 1); !errors.Is(e, ErrNotFound) {
		t.Fatalf("cross-archive preview %v", e)
	}
	if _, e = owner.Exec(`DELETE FROM invoyis.archive_memberships WHERE archive_id=$1 AND account_id=$2`, archive.ID, actor); e != nil {
		t.Fatal(e)
	}
	if _, e = second.previewLookup(ctx, actor, archive.ID, receipt.DocumentID, 1); !errors.Is(e, ErrNotFound) {
		t.Fatalf("revoked preview %v", e)
	}
}
