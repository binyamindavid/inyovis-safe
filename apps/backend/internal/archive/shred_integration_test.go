package archive

import (
	"context"
	"errors"
	"testing"
)

type failingDisposal struct {
	*fakeOriginals
	fail bool
}

func (f *failingDisposal) DeleteOriginal(_ context.Context, key string) error {
	if f.fail {
		return errors.New("storage unavailable")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	return nil
}
func (f *failingDisposal) OriginalExists(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[key]
	return ok, nil
}

func TestPersonalShredRestrictedRoleHoldAndIdempotency(t *testing.T) {
	s, _, owner, admin := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	disposal := &failingDisposal{fakeOriginals: store}
	s.Storage = disposal
	a, e := s.Create(ctx, admin, "personal", "Disposal fixture", "")
	if e != nil {
		t.Fatal(e)
	}
	doc, _, sha := clearedUpload(t, s, owner, store, admin, a.ID, "personal-shred")
	run, _ := newID()
	if _, e = owner.Exec(`INSERT INTO invoyis.extraction_runs(id,archive_id,document_id,stage,input_sha256,extractor_version,candidates)
		VALUES($1,$2,$3,'document_xml',$4,'synthetic-test','{}')`, run, a.ID, doc, sha); e != nil {
		t.Fatal(e)
	}
	key, _ := newID()
	_, e = s.WriteReview(ctx, admin, a.ID, doc, ReviewWrite{Action: "confirm", ExpectedVersion: 0, IdempotencyKey: key,
		SourceRunID: &run, Metadata: ReviewMetadata{DocumentType: TypeLease, Title: "Personal document", IssueDate: "2020-01-01"},
		AcknowledgedWarnings: []string{}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.processing_jobs SET state='succeeded' WHERE document_id=$1`, doc); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.upload_sessions SET grant_expires_at=now()-interval '1 second',finalise_by=now()-interval '1 second' WHERE archive_id=$1`, a.ID); e != nil {
		t.Fatal(e)
	}
	status, e := s.DocumentStatus(ctx, admin, a.ID, doc)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.MoveToTrash(ctx, admin, a.ID, doc, status.OriginalFilename, "test recovery"); e != nil {
		t.Fatal(e)
	}
	trashed, e := s.ShredEligibility(ctx, admin, a.ID, doc)
	if e != nil || trashed.Blocker != "in_trash" {
		t.Fatalf("trash: %+v %v", trashed, e)
	}
	if e = s.RestoreFromTrash(ctx, admin, a.ID, doc); e != nil {
		t.Fatal(e)
	}
	viewer := addAccount(t, owner, "shred-viewer@example.test")
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a.ID, viewer); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AuthorizeShred(ctx, viewer, a.ID, doc, status.OriginalFilename); !errors.Is(e, ErrForbidden) {
		t.Fatalf("viewer shred: %v", e)
	}
	forged, e := s.transaction(ctx, viewer, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	var direct string
	e = forged.QueryRowContext(ctx, `SELECT invoyis.authorize_document_shred($1,$2,$3,$4,NULL,'')`, a.ID, doc, status.OriginalFilename, "00000000-0000-4000-8000-000000000000").Scan(&direct)
	_ = forged.Rollback()
	if e == nil {
		t.Fatal("viewer directly invoked shred function")
	}
	other, e := s.Create(ctx, admin, "personal", "Other", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AuthorizeShred(ctx, admin, other.ID, doc, status.OriginalFilename); e == nil {
		t.Fatal("cross-archive shred succeeded")
	}
	if e = s.SetPersonalReminder(ctx, admin, a.ID, doc, ptrString("2000-01-01")); e != nil {
		t.Fatal(e)
	}
	hold, e := s.PlaceDocumentHold(ctx, admin, a.ID, doc, "active hold")
	if e != nil {
		t.Fatal(e)
	}
	state, e := s.AuthorizeShred(ctx, admin, a.ID, doc, status.OriginalFilename)
	if e != nil || state.Blocker != "active_hold" {
		t.Fatalf("hold: %+v %v", state, e)
	}
	if e = s.ReleaseDocumentHold(ctx, admin, a.ID, doc, hold.ID, "resolved"); e != nil {
		t.Fatal(e)
	}
	exportID, _ := newID()
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_exports(id,archive_id,storage_key,state,items,bundle_sha256,byte_count,expires_at) VALUES($1,$2,$3,'ready',jsonb_build_array(jsonb_build_object('documentId',$4::uuid)),repeat('a',64),1,now()+interval '1 day')`, exportID, a.ID, "exports/"+a.ID+"/"+exportID+".zip", doc); e != nil {
		t.Fatal(e)
	}
	exported, e := s.ShredEligibility(ctx, admin, a.ID, doc)
	if e != nil || exported.Blocker != "active_export" {
		t.Fatalf("export: %+v %v", exported, e)
	}
	if _, e = owner.Exec(`DELETE FROM invoyis.archive_exports WHERE id=$1`, exportID); e != nil {
		t.Fatal(e)
	}
	state, e = s.AuthorizeShred(ctx, admin, a.ID, doc, "wrong name")
	if e != nil || state.Blocker != "document_changed" {
		t.Fatalf("name: %+v %v", state, e)
	}
	state, e = s.AuthorizeShred(ctx, admin, a.ID, doc, status.OriginalFilename)
	if e != nil || state.Status != "pending" {
		t.Fatalf("authorize: %+v %v", state, e)
	}
	again, e := s.AuthorizeShred(ctx, admin, a.ID, doc, status.OriginalFilename)
	if e != nil || again.Status != "pending" {
		t.Fatalf("retry: %+v %v", again, e)
	}
	if _, e = s.PlaceDocumentHold(ctx, admin, a.ID, doc, "too late"); e == nil {
		t.Fatal("hold accepted after shredding authorization")
	}
	var count int
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.document_shreds WHERE archive_id=$1 AND document_id=$2`, a.ID, doc).Scan(&count); e != nil || count != 1 {
		t.Fatalf("audit count %d %v", count, e)
	}
	if _, e = s.DocumentStatus(ctx, admin, a.ID, doc); !errors.Is(e, ErrNotFound) {
		t.Fatalf("shredded document visible: %v", e)
	}
	// Failed storage deletion leaves a visible failure; a later attempt verifies
	// the exact key and completes the durable ledger.
	disposal.fail = true
	if found, e := s.ReconcileOneShred(ctx); !found || e == nil {
		t.Fatalf("storage failure found=%t err=%v", found, e)
	}
	failed, e := s.ShredEligibility(ctx, admin, a.ID, doc)
	if e != nil || failed.Status != "failed" {
		t.Fatalf("failure status: %+v %v", failed, e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.shred_objects SET next_attempt_at=now()-interval '1 second' WHERE document_id=$1`, doc); e != nil {
		t.Fatal(e)
	}
	disposal.fail = false
	// Crash after object removal but before the ledger acknowledgement: expire
	// the abandoned lease and let the next worker verify absence idempotently.
	var claimedLedger, claimedArchive, claimedDocument, claimedKey, claimedToken string
	if e = s.DB.QueryRowContext(ctx, `SELECT ledger_id,archive_id,document_id,storage_key,token FROM invoyis.claim_shred_object()`).Scan(&claimedLedger, &claimedArchive, &claimedDocument, &claimedKey, &claimedToken); e != nil {
		t.Fatal(e)
	}
	if claimedDocument != doc {
		t.Fatalf("claimed another document: %s", claimedDocument)
	}
	if claimedLedger == "" || claimedArchive != a.ID || claimedToken == "" {
		t.Fatal("invalid cleanup claim")
	}
	if e = disposal.DeleteOriginal(ctx, claimedKey); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.shred_objects SET lease_until=now()-interval '1 second' WHERE document_id=$1`, doc); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 8; i++ {
		found, runErr := s.ReconcileOneShred(ctx)
		if runErr != nil {
			t.Fatal(runErr)
		}
		if !found {
			break
		}
	}
	verified, e := s.ShredEligibility(ctx, admin, a.ID, doc)
	if e != nil || verified.Status != "verified" {
		t.Fatalf("verified: %+v %v", verified, e)
	}
	for _, table := range []string{"documents", "document_revisions", "retention_evaluations", "extraction_runs", "processing_jobs", "upload_receipts", "submissions", "stored_objects", "upload_sessions", "preview_derivatives", "document_holds", "personal_reminders", "document_trash_events"} {
		var left int
		if e = owner.QueryRow(`SELECT count(*) FROM invoyis.`+table+` WHERE archive_id=$1`, a.ID).Scan(&left); e != nil || left != 0 {
			t.Fatalf("%s still has %d rows: %v", table, left, e)
		}
	}
	var retainedKeys int
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.shred_objects WHERE archive_id=$1 AND storage_key IS NOT NULL`, a.ID).Scan(&retainedKeys); e != nil || retainedKeys != 0 {
		t.Fatalf("cleanup ledger retained %d keys: %v", retainedKeys, e)
	}
	store.mu.Lock()
	remaining := len(store.objects)
	store.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d object-store keys remain", remaining)
	}
}

func ptrString(v string) *string { return &v }

func TestBusinessShredRequiresFreshValidatedDecision(t *testing.T) {
	s, owner, admin, a, d := retentionFixture(t)
	ctx := context.Background()
	if _, e := owner.Exec(`UPDATE invoyis.processing_jobs SET state='succeeded' WHERE document_id=$1`, d); e != nil {
		t.Fatal(e)
	}
	status, e := s.DocumentStatus(ctx, admin, a, d)
	if e != nil {
		t.Fatal(e)
	}
	initial, e := s.ShredEligibility(ctx, admin, a, d)
	if e != nil || initial.Blocker != "retention_missing" {
		t.Fatalf("missing: %+v %v", initial, e)
	}
	if _, e = s.SetRetentionDocumentFacts(ctx, admin, a, d, 0, "company", 0, ""); e != nil {
		t.Fatal(e)
	}
	version, _ := newID()
	if _, e = owner.Exec(`INSERT INTO invoyis.retention_rule_sets(version) VALUES($1)`, version); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.retention_rule_versions(rule_id,version,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active) VALUES($1,1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','synthetic shredding','issue_date',0,true)`, version+"rule", version); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.retention_coverage(coverage_id,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,status,source_reference) VALUES($1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','complete','synthetic shredding')`, version+"coverage", version); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.retention_active_rule_set SET version=$1 WHERE singleton=true`, version); e != nil {
		t.Fatal(e)
	}
	stale, e := s.ShredEligibility(ctx, admin, a, d)
	if e != nil || stale.Blocker != "retention_missing" {
		t.Fatalf("unassessed: %+v %v", stale, e)
	}
	if _, e = s.EvaluateRetention(ctx, admin, a, d); e != nil {
		t.Fatal(e)
	}
	eligible, e := s.ShredEligibility(ctx, admin, a, d)
	if e != nil || eligible.Status != "eligible" {
		t.Fatalf("eligible: %+v %v", eligible, e)
	}
	hold, e := s.PlaceDocumentHold(ctx, admin, a, d, "legal hold")
	if e != nil {
		t.Fatal(e)
	}
	blocked, e := s.AuthorizeShred(ctx, admin, a, d, status.OriginalFilename)
	if e != nil || blocked.Blocker != "active_hold" {
		t.Fatalf("hold race: %+v %v", blocked, e)
	}
	if e = s.ReleaseDocumentHold(ctx, admin, a, d, hold.ID, "cleared"); e != nil {
		t.Fatal(e)
	}
	accepted, e := s.AuthorizeShred(ctx, admin, a, d, status.OriginalFilename)
	if e != nil || accepted.Status != "pending" {
		t.Fatalf("business authorize: %+v %v", accepted, e)
	}
}

func TestShredPreservesSharedOriginal(t *testing.T) {
	s, _, owner, admin := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = &failingDisposal{fakeOriginals: store}
	a, e := s.Create(ctx, admin, "personal", "Shared original", "")
	if e != nil {
		t.Fatal(e)
	}
	first, _, _ := clearedUpload(t, s, owner, store, admin, a.ID, "same-bytes")
	_, e = write(t, s, admin, a.ID, first, "confirm", 0, ReviewMetadata{DocumentType: TypeLease, Title: "First", IssueDate: "2020-01-01"})
	if e != nil {
		t.Fatal(e)
	}
	second, _, _ := clearedUpload(t, s, owner, store, admin, a.ID, "second-session")
	// Simulate a canonical shared original: both document submissions reference
	// the same stored object after duplicate consolidation.
	if _, e = owner.Exec(`UPDATE invoyis.submissions SET stored_object_id=(SELECT s.stored_object_id FROM invoyis.documents d JOIN invoyis.submissions s ON s.id=d.submission_id WHERE d.id=$1) WHERE id=(SELECT submission_id FROM invoyis.documents WHERE id=$2)`, first, second); e != nil {
		t.Fatal(e)
	}
	_, e = write(t, s, admin, a.ID, second, "confirm", 0, ReviewMetadata{DocumentType: TypeLease, Title: "Second", IssueDate: "2020-01-01"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.processing_jobs SET state='succeeded' WHERE document_id IN ($1,$2)`, first, second); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.upload_sessions SET grant_expires_at=now()-interval '1 second',finalise_by=now()-interval '1 second' WHERE archive_id=$1`, a.ID); e != nil {
		t.Fatal(e)
	}
	before, e := s.DocumentStatus(ctx, admin, a.ID, first)
	if e != nil {
		t.Fatal(e)
	}
	result, e := s.AuthorizeShred(ctx, admin, a.ID, first, before.OriginalFilename)
	if e != nil || result.Status != "pending" {
		t.Fatalf("authorize %+v %v", result, e)
	}
	var count int
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.shred_objects WHERE document_id=$1 AND stored_object_id IS NOT NULL AND status='pending'`, first).Scan(&count); e != nil || count != 0 {
		t.Fatalf("shared object scheduled: %d %v", count, e)
	}
	for i := 0; i < 8; i++ {
		found, runErr := s.ReconcileOneShred(ctx)
		if runErr != nil {
			t.Fatal(runErr)
		}
		if !found {
			break
		}
	}
	firstResult, e := s.ShredEligibility(ctx, admin, a.ID, first)
	if e != nil || firstResult.Status != "verified" {
		t.Fatalf("first cleanup: %+v %v", firstResult, e)
	}
	if _, e = s.DocumentStatus(ctx, admin, a.ID, second); e != nil {
		t.Fatalf("other document lost: %v", e)
	}
	secondStatus, e := s.DocumentStatus(ctx, admin, a.ID, second)
	if e != nil {
		t.Fatal(e)
	}
	secondResult, e := s.AuthorizeShred(ctx, admin, a.ID, second, secondStatus.OriginalFilename)
	if e != nil || secondResult.Status != "pending" {
		t.Fatalf("second authorize: %+v %v", secondResult, e)
	}
	for i := 0; i < 8; i++ {
		found, runErr := s.ReconcileOneShred(ctx)
		if runErr != nil {
			t.Fatal(runErr)
		}
		if !found {
			break
		}
	}
	secondResult, e = s.ShredEligibility(ctx, admin, a.ID, second)
	if e != nil || secondResult.Status != "verified" {
		t.Fatalf("second cleanup: %+v %v", secondResult, e)
	}
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.stored_objects WHERE archive_id=$1`, a.ID).Scan(&count); e != nil || count != 0 {
		t.Fatalf("shared object metadata remains: %d %v", count, e)
	}
	store.mu.Lock()
	remaining := len(store.objects)
	store.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d object-store keys remain after last shred", remaining)
	}
}
