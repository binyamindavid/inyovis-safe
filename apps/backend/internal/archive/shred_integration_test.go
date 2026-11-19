package archive

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

type failingDisposal struct {
	OriginalStorage
	DisposalStorage
	fail bool
}

func (f *failingDisposal) DeleteOriginal(ctx context.Context, key string) error {
	if f.fail {
		return errors.New("storage unavailable")
	}
	return f.DisposalStorage.DeleteOriginal(ctx, key)
}

type memoryDisposal struct {
	*fakeOriginals
}

func (m memoryDisposal) DeleteOriginal(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func (m memoryDisposal) OriginalExists(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objects[key]
	return ok, nil
}

func trashShredTestStorage(t *testing.T) *failingDisposal {
	t.Helper()
	if os.Getenv("SHRED_OBJECT_STORAGE_TEST") != "1" {
		store := memoryDisposal{&fakeOriginals{objects: map[string][]byte{}}}
		return &failingDisposal{OriginalStorage: store, DisposalStorage: store}
	}
	store, err := StorageFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	suffix, _ := newID()
	store.bucket = "invoyis-trash-test-" + suffix[:8]
	if err = store.client.MakeBucket(ctx, store.bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for item := range store.client.ListObjects(ctx, store.bucket, minio.ListObjectsOptions{Recursive: true}) {
			if item.Err != nil {
				t.Error(item.Err)
				break
			}
			if err := store.DeleteOriginal(ctx, item.Key); err != nil {
				t.Error(err)
			}
		}
		if err := store.client.RemoveBucket(ctx, store.bucket); err != nil {
			t.Error(err)
		}
	})
	return &failingDisposal{OriginalStorage: store, DisposalStorage: store}
}

func TestPersonalShredRestrictedRoleHoldAndIdempotency(t *testing.T) {
	s, _, owner, admin := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	disposal := &failingDisposal{OriginalStorage: store, DisposalStorage: memoryDisposal{store}}
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
	if e != nil || trashed.Status != "eligible" {
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
	store := trashShredTestStorage(t)
	s.Storage = store
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
	var sharedKey string
	if e = owner.QueryRow(`SELECT o.storage_key FROM invoyis.documents d JOIN invoyis.submissions s ON s.id=d.submission_id
		JOIN invoyis.stored_objects o ON o.id=s.stored_object_id WHERE d.id=$1`, first).Scan(&sharedKey); e != nil {
		t.Fatal(e)
	}
	before, e := s.DocumentStatus(ctx, admin, a.ID, first)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.MoveToTrash(ctx, admin, a.ID, first, before.OriginalFilename, "shared original"); e != nil {
		t.Fatal(e)
	}
	preview, e := s.previewTrashShred(ctx, admin, a.ID, first)
	if e != nil || preview.Eligible != 1 {
		t.Fatalf("preview %+v %v", preview, e)
	}
	results, e := s.authorizeTrashShred(ctx, admin, a.ID, []TrashShredTarget{preview.Items[0].TrashShredTarget})
	if e != nil || results[0].Status != "pending" {
		t.Fatalf("authorize %+v %v", results, e)
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
	if exists, err := store.OriginalExists(ctx, sharedKey); err != nil || !exists {
		t.Fatalf("shared original missing after first shred: %t %v", exists, err)
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
	if realStore, ok := store.OriginalStorage.(*S3Storage); ok {
		for object := range realStore.client.ListObjects(ctx, realStore.bucket, minio.ListObjectsOptions{Recursive: true, WithVersions: true}) {
			t.Fatalf("object remains after last shred: %s %v", object.Key, object.Err)
		}
	} else if remaining := len(store.OriginalStorage.(memoryDisposal).objects); remaining != 0 {
		t.Fatalf("%d object-store keys remain after last shred", remaining)
	}
}

func TestTrashShredMixedBatchRechecksConfirmedTargets(t *testing.T) {
	s, _, owner, admin := setup(t)
	ctx := context.Background()
	disposal := trashShredTestStorage(t)
	s.Storage = disposal
	a, err := s.Create(ctx, admin, "personal", "Mixed Trash", "")
	if err != nil {
		t.Fatal(err)
	}
	upload := func(label string, confirm bool) string {
		t.Helper()
		doc, _, _ := clearedUpload(t, s, owner, disposal, admin, a.ID, label)
		if confirm {
			if _, err := write(t, s, admin, a.ID, doc, "confirm", 0, ReviewMetadata{DocumentType: TypeLease, Title: label, IssueDate: "2020-01-01"}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := owner.Exec(`UPDATE invoyis.processing_jobs SET state='succeeded' WHERE document_id=$1`, doc); err != nil {
			t.Fatal(err)
		}
		status, err := s.DocumentStatus(ctx, admin, a.ID, doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.MoveToTrash(ctx, admin, a.ID, doc, status.OriginalFilename, "mixed test"); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	eligible := upload("eligible", true)
	held := upload("held", true)
	restored := upload("restored", true)
	changed := upload("changed", true)
	unconfirmed := upload("unconfirmed", false)
	if _, err := owner.Exec(`UPDATE invoyis.documents SET recovery_until=now()-interval '1 day' WHERE id IN ($1,$2)`, eligible, unconfirmed); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.upload_sessions SET grant_expires_at=now()-interval '1 second',finalise_by=now()-interval '1 second' WHERE archive_id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	preview, err := s.previewTrashShred(ctx, admin, a.ID, "")
	if err != nil || preview.Eligible != 4 || preview.Blocked != 1 {
		t.Fatalf("preview %+v %v", preview, err)
	}
	byID := map[string]TrashShredTarget{}
	for _, item := range preview.Items {
		if item.Status == "eligible" {
			byID[item.ID] = item.TrashShredTarget
		} else if item.ID != unconfirmed || item.Blocker != "cleared_confirmed_document_required" {
			t.Fatalf("unexpected blocker %+v", item)
		}
	}
	contributor := addAccount(t, owner, "trash-contributor@example.test")
	if _, err = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'contributor')`, a.ID, contributor); err != nil {
		t.Fatal(err)
	}
	if _, err = s.previewTrashShred(ctx, contributor, a.ID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("contributor preview: %v", err)
	}
	if _, err = s.authorizeTrashShred(ctx, contributor, a.ID, []TrashShredTarget{byID[eligible]}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("contributor authorization: %v", err)
	}
	// Change each authorization input since preview, including a restore followed
	// by a new trash entry and a replacement revision that remains confirmed.
	if err = s.RestoreFromTrash(ctx, admin, a.ID, restored); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveToTrash(ctx, admin, a.ID, restored, byID[restored].Name, "new Trash entry"); err != nil {
		t.Fatal(err)
	}
	if err = s.RestoreFromTrash(ctx, admin, a.ID, changed); err != nil {
		t.Fatal(err)
	}
	if _, err = write(t, s, admin, a.ID, changed, "confirm", 1, ReviewMetadata{DocumentType: TypeLease, Title: "Changed revision", IssueDate: "2020-01-02"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveToTrash(ctx, admin, a.ID, changed, byID[changed].Name, "new revision"); err != nil {
		t.Fatal(err)
	}
	// Use the current Trash timestamp to isolate the revision mismatch.
	current, err := s.previewTrashShred(ctx, admin, a.ID, changed)
	if err != nil || current.Eligible != 1 {
		t.Fatalf("changed preview %+v %v", current, err)
	}
	stale := byID[changed]
	stale.TrashedAt = current.Items[0].TrashedAt
	targets := []TrashShredTarget{byID[held], byID[restored], stale, byID[eligible]}
	type attempt struct {
		results []TrashShredEvent
		err     error
	}
	// Hold the document row while the two authorizations start, then commit a
	// hold. Each request must observe that hold when it acquires the row lock.
	holdTx, err := owner.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holdTx.Rollback()
	var blockerPID int
	if err = holdTx.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM invoyis.documents WHERE id=$1 FOR UPDATE`, held).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = holdTx.ExecContext(ctx, `INSERT INTO invoyis.document_holds(id,archive_id,document_id,reason,placed_by)
		VALUES(gen_random_uuid(),$1,$2,'hold during authorization',$3)`, a.ID, held, admin); err != nil {
		t.Fatal(err)
	}
	finished := make(chan attempt, 2)
	for range 2 {
		go func() {
			results, err := s.authorizeTrashShred(ctx, admin, a.ID, targets)
			finished <- attempt{results, err}
		}()
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		err = owner.QueryRowContext(wait, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE $1=ANY(pg_blocking_pids(pid)))`, blockerPID).Scan(&waiting)
		if err != nil {
			t.Fatalf("authorization did not wait for the document lock: %v", err)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = holdTx.Commit(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got := <-finished
		if got.err != nil || len(got.results) != 4 || got.results[0].Blocker != "active_hold" ||
			got.results[1].Blocker != "document_changed" || got.results[2].Blocker != "document_changed" || got.results[3].Status != "pending" {
			t.Fatalf("batch %+v %v", got.results, got.err)
		}
	}
	list, err := s.readTrash(ctx, admin, a.ID, false)
	if err != nil || len(list.Items) != 4 || len(list.Shredding) != 1 || list.Shredding[0].Status != "pending" {
		t.Fatalf("Trash %+v %v", list, err)
	}

	if _, err = owner.Exec(`UPDATE invoyis.shred_objects SET next_attempt_at=now() WHERE document_id=$1`, eligible); err != nil {
		t.Fatal(err)
	}
	disposal.fail = true
	if found, err := s.ReconcileOneShred(ctx); !found || err == nil || !strings.Contains(err.Error(), "storage_delete_failed") {
		t.Fatalf("failed cleanup found=%t err=%v", found, err)
	}
	list, err = s.readTrash(ctx, admin, a.ID, false)
	if err != nil || len(list.Shredding) != 1 || list.Shredding[0].Status != "failed" {
		t.Fatalf("failed history %+v %v", list, err)
	}
	disposal.fail = false
	if _, err = owner.Exec(`UPDATE invoyis.shred_objects SET next_attempt_at=now() WHERE document_id=$1`, eligible); err != nil {
		t.Fatal(err)
	}
	for {
		found, err := s.ReconcileOneShred(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
	}
	list, err = s.readTrash(ctx, admin, a.ID, false)
	if err != nil || len(list.Shredding) != 1 || list.Shredding[0].Status != "verified" {
		t.Fatalf("verified history %+v %v", list, err)
	}
}
