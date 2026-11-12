package archive

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
)

func TestLikelyDuplicateRevisionBoundReview(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	a, err := s.Create(ctx, actor, "personal", "Likely duplicate review", "")
	if err != nil {
		t.Fatal(err)
	}
	otherArchive, err := s.Create(ctx, actor, "personal", "Other likely archive", "")
	if err != nil {
		t.Fatal(err)
	}
	supplier, _, err := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Électricité")
	if err != nil {
		t.Fatal(err)
	}
	left, leftBytes, _ := clearedUpload(t, s, owner, store, actor, a.ID, "left-original")
	right, rightBytes, _ := clearedUpload(t, s, owner, store, actor, a.ID, "right-original")
	meta := ReviewMetadata{DocumentType: "invoice", Supplier: "Électricité printed", InvoiceID: "00042-A", IssueDate: "2026-11-12", Currency: "ILS", Total: amount("100.00"), TaxLines: []TaxLine{}}
	filing := ReviewFiling{SupplierID: &supplier.ID}
	if _, err = reviewFilingWrite(t, s, actor, a.ID, left, "confirm", 0, meta, filing); err != nil {
		t.Fatal(err)
	}
	if items, e := s.ListDuplicateCandidates(ctx, actor, a.ID); e != nil || len(items) != 0 {
		t.Fatalf("one confirmed: %v %+v", e, items)
	}
	if _, err = reviewFilingWrite(t, s, actor, a.ID, right, "draft", 0, meta, filing); err != nil {
		t.Fatal(err)
	}
	if items, e := s.ListDuplicateCandidates(ctx, actor, a.ID); e != nil || len(items) != 0 {
		t.Fatalf("draft queued: %v %+v", e, items)
	}
	confirmed, err := reviewFilingWrite(t, s, actor, a.ID, right, "confirm", 1, meta, filing)
	if err != nil || confirmed.Confirmed == nil {
		t.Fatalf("second confirmation: %v", err)
	}
	items, err := s.ListDuplicateCandidates(ctx, actor, a.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("candidate: %v %+v", err, items)
	}
	c := items[0]
	if c.Left.DocumentID == c.Right.DocumentID || len(c.MatchedFields) != 5 || c.Left.Metadata.InvoiceID != "00042-A" || c.Right.Metadata.Supplier != "Électricité printed" {
		t.Fatalf("comparison: %+v", c)
	}
	viewer := addAccount(t, owner, "duplicate-viewer@example.test")
	stranger := addAccount(t, owner, "duplicate-stranger@example.test")
	if _, err = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a.ID, viewer); err != nil {
		t.Fatal(err)
	}
	if got, e := s.ListDuplicateCandidates(ctx, viewer, a.ID); e != nil || len(got) != 1 {
		t.Fatalf("viewer queue: %v %+v", e, got)
	}
	if _, e := s.ListDuplicateCandidates(ctx, stranger, a.ID); !errors.Is(e, ErrNotFound) {
		t.Fatalf("stranger queue: %v", e)
	}
	input := ResolveDuplicateInput{Decision: "duplicate", LeftRevisionID: c.Left.RevisionID, RightRevisionID: c.Right.RevisionID}
	if _, e := s.ResolveDuplicate(ctx, viewer, a.ID, c.ID, input); !errors.Is(e, ErrForbidden) {
		t.Fatalf("viewer resolved: %v", e)
	}
	if _, e := s.ResolveDuplicate(ctx, actor, otherArchive.ID, c.ID, input); !errors.Is(e, ErrNotFound) {
		t.Fatalf("cross-archive resolved: %v", e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.ResolveDuplicate(ctx, actor, a.ID, c.ID, input); results <- e }()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatalf("same decision retry: %v", e)
		}
	}
	var audits int
	if err = owner.QueryRow(`SELECT count(*) FROM invoyis.archive_audit WHERE archive_id=$1 AND action='duplicate_candidate_resolved'`, a.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit count: %d %v", audits, err)
	}
	if got, e := s.ListDuplicateCandidates(ctx, actor, a.ID); e != nil || len(got) != 0 {
		t.Fatalf("resolved queue: %v %+v", e, got)
	}
	for _, original := range []struct {
		id   string
		data []byte
	}{{left, leftBytes}, {right, rightBytes}} {
		got, _, e := s.DownloadOriginal(ctx, viewer, a.ID, original.id)
		if e != nil || !bytes.Equal(got, original.data) {
			t.Fatalf("original %s changed: %v", original.id, e)
		}
	}
	// A new confirmation invalidates the prior compared revisions. Changing
	// currency removes eligibility; confirming matching values queues a fresh review.
	changed := meta
	changed.Currency = "EUR"
	if _, err = reviewFilingWrite(t, s, actor, a.ID, right, "confirm", 2, changed, filing); err != nil {
		t.Fatal(err)
	}
	if got, e := s.ListDuplicateCandidates(ctx, actor, a.ID); e != nil || len(got) != 0 {
		t.Fatalf("stale pair: %v %+v", e, got)
	}
	if _, e := s.ResolveDuplicate(ctx, actor, a.ID, c.ID, input); !errors.Is(e, ErrConflict) {
		t.Fatalf("stale resolution: %v", e)
	}
	if _, err = reviewFilingWrite(t, s, actor, a.ID, right, "confirm", 3, meta, filing); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListDuplicateCandidates(ctx, actor, a.ID)
	if err != nil || len(got) != 1 || got[0].ID != c.ID || got[0].Right.RevisionID == c.Right.RevisionID && got[0].Left.RevisionID == c.Left.RevisionID {
		t.Fatalf("reconfirmed pair: %v %+v", err, got)
	}
	keep := ResolveDuplicateInput{Decision: "keep_both", LeftRevisionID: got[0].Left.RevisionID, RightRevisionID: got[0].Right.RevisionID}
	contributor := addAccount(t, owner, "duplicate-resolver@example.test")
	if _, err = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'contributor')`, a.ID, contributor); err != nil {
		t.Fatal(err)
	}
	conflicting := ResolveDuplicateInput{Decision: "duplicate", LeftRevisionID: keep.LeftRevisionID, RightRevisionID: keep.RightRevisionID}
	outcomes := make(chan error, 2)
	wg = sync.WaitGroup{}
	for _, choice := range []struct {
		actor int64
		input ResolveDuplicateInput
	}{{actor, keep}, {contributor, conflicting}} {
		wg.Add(1)
		go func(actor int64, input ResolveDuplicateInput) {
			defer wg.Done()
			_, e := s.ResolveDuplicate(ctx, actor, a.ID, c.ID, input)
			outcomes <- e
		}(choice.actor, choice.input)
	}
	wg.Wait()
	close(outcomes)
	wins, conflicts := 0, 0
	for e := range outcomes {
		if e == nil {
			wins++
		} else if errors.Is(e, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("racing decisions: wins=%d conflicts=%d", wins, conflicts)
	}
	if err = owner.QueryRow(`SELECT count(*) FROM invoyis.archive_audit WHERE archive_id=$1 AND action='duplicate_candidate_resolved'`, a.ID).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("redecision audit: %d %v", audits, err)
	}
}

func TestLikelyDuplicateEligibility(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	a, err := s.Create(ctx, actor, "personal", "Eligibility", "")
	if err != nil {
		t.Fatal(err)
	}
	supplier, _, err := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "Supplier")
	if err != nil {
		t.Fatal(err)
	}
	base := ReviewMetadata{DocumentType: "invoice", Supplier: "Printed Supplier", InvoiceID: "007", IssueDate: "2026-11-12", Currency: "ILS", Total: amount("12.00"), TaxLines: []TaxLine{}}
	linked := ReviewFiling{SupplierID: &supplier.ID}
	first, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "eligibility-first")
	if _, err = reviewFilingWrite(t, s, actor, a.ID, first, "confirm", 0, base, linked); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		metadata ReviewMetadata
		filing   ReviewFiling
	}{
		{"unlinked", base, ReviewFiling{}},
		{"different-currency", func() ReviewMetadata { m := base; m.Currency = "EUR"; return m }(), linked},
		{"missing-number", func() ReviewMetadata { m := base; m.InvoiceID = ""; return m }(), linked},
		{"missing-date", func() ReviewMetadata { m := base; m.IssueDate = ""; return m }(), linked},
		{"missing-total", func() ReviewMetadata { m := base; m.Total = nil; return m }(), linked},
	}
	for _, tc := range cases {
		doc, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, tc.name)
		if tc.name == "missing-number" || tc.name == "missing-date" || tc.name == "missing-total" {
			// Required confirmation fields reject a missing printed number, so an
			// incomplete draft must never enter the queue.
			if _, err = reviewFilingWrite(t, s, actor, a.ID, doc, "draft", 0, tc.metadata, tc.filing); err != nil {
				t.Fatal(err)
			}
		} else if _, err = reviewFilingWrite(t, s, actor, a.ID, doc, "confirm", 0, tc.metadata, tc.filing); err != nil {
			t.Fatal(err)
		}
	}
	if got, e := s.ListDuplicateCandidates(ctx, actor, a.ID); e != nil || len(got) != 0 {
		t.Fatalf("ineligible pair: %v %+v", e, got)
	}
}

func TestLikelyDuplicateQueueRefillsBeyondFirstHundred(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	archive, err := s.Create(ctx, actor, "personal", "Long duplicate queue", "")
	if err != nil {
		t.Fatal(err)
	}
	supplier, _, err := s.CreateOrganisation(ctx, actor, archive.ID, "suppliers", "Queue supplier")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := owner.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO invoyis.upload_sessions(id,archive_id,uploader_id,idempotency_key,filename,media_type,declared_bytes,declared_sha256,staging_key,prepared_key,final_key,grant_expires_at,finalise_by,state,verified_bytes,verified_sha256)
 SELECT md5('session'||n)::uuid,$1,$2,'queue-'||n,'queue-'||n||'.pdf','application/pdf',7,repeat(md5(n::text),2),'staging/queue/'||n,'prepared/queue/'||n,'originals/queue/'||n,now(),now(),'complete',7,repeat(md5(n::text),2) FROM generate_series(1,102) n`, []any{archive.ID, actor}},
		{`INSERT INTO invoyis.stored_objects(id,archive_id,upload_session_id,storage_key,byte_count,sha256)
 SELECT md5('object'||n)::uuid,$1,md5('session'||n)::uuid,'originals/queue/'||n,7,repeat(md5(n::text),2) FROM generate_series(1,102) n`, []any{archive.ID}},
		{`INSERT INTO invoyis.submissions(id,archive_id,upload_session_id,stored_object_id,uploader_id,source,original_filename)
 SELECT md5('submission'||n)::uuid,$1,md5('session'||n)::uuid,md5('object'||n)::uuid,$2,'browser_upload','queue-'||n||'.pdf' FROM generate_series(1,102) n`, []any{archive.ID, actor}},
		{`INSERT INTO invoyis.documents(id,archive_id,submission_id,state,clearance)
 SELECT md5('document'||n)::uuid,$1,md5('submission'||n)::uuid,'ready','cleared' FROM generate_series(1,102) n`, []any{archive.ID}},
		{`INSERT INTO invoyis.document_revisions(id,archive_id,document_id,actor_id,kind,metadata,provenance,issue_date,total,supplier_id)
 SELECT md5('revision'||n)::uuid,$1,md5('document'||n)::uuid,$2,'confirmed',
 '{"documentType":"invoice","supplier":"Printed","invoiceId":"001","issueDate":"2026-11-12","currency":"ILS","total":"100.00","taxLines":[]}'::jsonb,'{}'::jsonb,'2026-11-12',100,$3
 FROM generate_series(1,102) n`, []any{archive.ID, actor, supplier.ID}},
		{`UPDATE invoyis.documents SET current_confirmed_revision_id=md5('revision'||n)::uuid
 FROM generate_series(1,102) n WHERE id=md5('document'||n)::uuid AND archive_id=$1`, []any{archive.ID}},
		{`INSERT INTO invoyis.duplicate_candidates(id,archive_id,left_document_id,right_document_id,left_revision_id,right_revision_id)
 SELECT md5('candidate'||n)::uuid,$1,
 least(md5('document1')::uuid,md5('document'||n)::uuid),greatest(md5('document1')::uuid,md5('document'||n)::uuid),
 CASE WHEN md5('document1')::uuid<md5('document'||n)::uuid THEN md5('revision1')::uuid ELSE md5('revision'||n)::uuid END,
 CASE WHEN md5('document1')::uuid<md5('document'||n)::uuid THEN md5('revision'||n)::uuid ELSE md5('revision1')::uuid END
 FROM generate_series(2,102) n`, []any{archive.ID}},
	}
	for _, statement := range statements {
		if _, err = tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	first, err := s.ListDuplicateCandidates(ctx, actor, archive.ID)
	if err != nil || len(first) != 100 {
		t.Fatalf("first page: %d %v", len(first), err)
	}
	firstIDs := make(map[string]bool, len(first))
	for _, candidate := range first {
		firstIDs[candidate.ID] = true
	}
	chosen := first[0]
	_, err = s.ResolveDuplicate(ctx, actor, archive.ID, chosen.ID, ResolveDuplicateInput{Decision: "keep_both", LeftRevisionID: chosen.Left.RevisionID, RightRevisionID: chosen.Right.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := s.ListDuplicateCandidates(ctx, actor, archive.ID)
	if err != nil || len(refreshed) != 100 {
		t.Fatalf("refreshed page: %d %v", len(refreshed), err)
	}
	newlyVisible := 0
	for _, candidate := range refreshed {
		if candidate.ID == chosen.ID {
			t.Fatal("resolved candidate remained in queue")
		}
		if !firstIDs[candidate.ID] {
			newlyVisible++
		}
	}
	if newlyVisible != 1 {
		t.Fatalf("expected candidate 101 after refresh, got %d", newlyVisible)
	}
}
