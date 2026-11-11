package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

type fakeOriginals struct {
	mu        sync.Mutex
	objects   map[string][]byte
	readError error
}

func (f *fakeOriginals) Grant(_ context.Context, key string, expiry time.Time, _ int64) (UploadGrant, error) {
	return UploadGrant{Method: "POST", URL: "https://storage.example.test/private", Fields: map[string]string{"key": key}, Headers: map[string]string{}, ExpiresAt: expiry.Format(time.RFC3339)}, nil
}
func (f *fakeOriginals) Read(_ context.Context, key string, cap int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readError != nil {
		return nil, f.readError
	}
	data, ok := f.objects[key]
	if !ok {
		return nil, ErrObjectMissing
	}
	if int64(len(data)) > cap {
		return nil, ErrInvalid
	}
	return bytes.Clone(data), nil
}
func TestUploadStagingOutcomes(t *testing.T) {
	s, _, owner, admin := setup(t)
	ctx := context.Background()
	storage := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = storage
	archive, err := s.Create(ctx, admin, "personal", "Synthetic staging", "")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("%PDF-1.7\nsynthetic\n%%EOF")
	session, err := s.CreateUpload(ctx, admin, archive.ID, claim(data, "staging-outcomes"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteUpload(ctx, admin, archive.ID, session.ID, false); !errors.Is(err, ErrStagedObjectMissing) {
		t.Fatalf("missing staging: %v", err)
	}
	if _, err = s.CompleteUpload(ctx, admin, "00000000-0000-4000-8000-000000000001", session.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inaccessible: %v", err)
	}
	storage.objects[session.Grant.Fields["key"]] = []byte("wrong")
	if _, err = s.CompleteUpload(ctx, admin, archive.ID, session.ID, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid bytes: %v", err)
	}
	storage.readError = errors.New("temporary storage outage")
	if _, err = s.CompleteUpload(ctx, admin, archive.ID, session.ID, false); err == nil || errors.Is(err, ErrStagedObjectMissing) {
		t.Fatalf("transient read: %v", err)
	}
	storage.readError = nil
	storage.objects[session.Grant.Fields["key"]] = data
	first, err := s.CompleteUpload(ctx, admin, archive.ID, session.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.CompleteUpload(ctx, admin, archive.ID, session.ID, false)
	if err != nil || replay.DocumentID != first.DocumentID || replay.ID != first.ID {
		t.Fatalf("replay: %#v %v", replay, err)
	}
	for _, table := range []string{"submissions", "processing_jobs"} {
		var count int
		if err := owner.QueryRow(fmt.Sprintf("SELECT count(*) FROM invoyis.%s WHERE archive_id=$1", table), archive.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s: %d %v", table, count, err)
		}
	}
}
func (f *fakeOriginals) PutOriginal(_ context.Context, key string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.objects[key]; ok {
		return ErrConflict
	}
	f.objects[key] = bytes.Clone(data)
	return nil
}
func claim(data []byte, key string) UploadClaim {
	sum := sha256.Sum256(data)
	return UploadClaim{IdempotencyKey: key, Filename: "חשבונית.pdf", MediaType: "application/pdf", ByteCount: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
}
func TestUploadIntakeRecoveryAndPermissions(t *testing.T) {
	s, _, owner, admin := setup(t)
	ctx := context.Background()
	storage := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = storage
	archive, err := s.Create(ctx, admin, "personal", "Synthetic archive", "")
	if err != nil {
		t.Fatal(err)
	}
	member := addAccount(t, owner, "member@example.test")
	viewer := addAccount(t, owner, "viewer@example.test")
	for _, m := range []struct {
		id   int64
		role string
	}{{member, "contributor"}, {viewer, "viewer"}} {
		_, err = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,$3)`, archive.ID, m.id, m.role)
		if err != nil {
			t.Fatal(err)
		}
	}
	data := []byte("%PDF-1.7\nsynthetic invoice <xml>same bytes</xml>\n%%EOF")
	c := claim(data, "same-key")
	if _, err = s.CreateUpload(ctx, viewer, archive.ID, c); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer: %v", err)
	}
	other, err := s.Create(ctx, admin, "personal", "Other archive", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateUpload(ctx, member, other.ID, c); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong archive: %v", err)
	}
	first, err := s.CreateUpload(ctx, member, archive.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.CreateUpload(ctx, member, archive.ID, c)
	if err != nil || retry.ID != first.ID {
		t.Fatalf("idempotent retry: %v %#v", err, retry)
	}
	changed := c
	changed.SHA256 = claim([]byte("different"), "same-key").SHA256
	if _, err = s.CreateUpload(ctx, member, archive.ID, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("fingerprint conflict: %v", err)
	}
	storage.objects[first.Grant.Fields["key"]] = data
	interrupted := false
	s.AfterFinalWrite = func() error {
		if !interrupted {
			interrupted = true
			return errors.New("synthetic crash after durable final write")
		}
		return nil
	}
	if _, err = s.CompleteUpload(ctx, member, archive.ID, first.ID, false); err == nil {
		t.Fatal("expected injected crash")
	}
	var state string
	err = owner.QueryRow(`SELECT state FROM invoyis.upload_sessions WHERE id=$1`, first.ID).Scan(&state)
	if err != nil || state != "finalising" {
		t.Fatalf("intent: %v %s", err, state)
	}
	// Simulate restart: a new service instance retains only the database and storage.
	restarted, err := New(os.Getenv("TEST_ARCHIVE_DATABASE_URL"), s.Auth, s.Sender, s.BrowserOrigin)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.Storage = storage
	_, err = owner.Exec(`UPDATE invoyis.upload_sessions SET finalising_at=now()-interval '1 minute' WHERE id=$1`, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	discoveryURL := os.Getenv("UPLOAD_RECONCILER_TEST_DATABASE_URL")
	if discoveryURL == "" {
		t.Skip("reconciler role URL missing")
	}
	discovery, err := sql.Open("pgx", discoveryURL)
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.Close()
	done, err := restarted.ReconcileUploads(ctx, discovery)
	if err != nil || done != 1 {
		t.Fatalf("reconcile: %d %v", done, err)
	}
	receipt, err := restarted.CompleteUpload(ctx, member, archive.ID, first.ID, false)
	if err != nil || receipt.SHA256 != c.SHA256 {
		t.Fatalf("stable receipt: %v %#v", err, receipt)
	}
	for table := range map[string]bool{"stored_objects": true, "submissions": true, "documents": true, "processing_jobs": true, "upload_receipts": true} {
		var count int
		err = owner.QueryRow(fmt.Sprintf(`SELECT count(*) FROM invoyis.%s WHERE archive_id=$1`, table), archive.ID).Scan(&count)
		if err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	var actor string
	err = owner.QueryRow(`SELECT system_actor FROM invoyis.archive_audit WHERE action='upload_received' AND archive_id=$1`, archive.ID).Scan(&actor)
	if err != nil || actor != "upload_reconciler" {
		t.Fatalf("audit actor: %q %v", actor, err)
	}
	second, err := s.CreateUpload(ctx, member, archive.ID, claim(data, "second"))
	if err != nil {
		t.Fatal(err)
	}
	storage.objects[second.Grant.Fields["key"]] = data
	if _, err = s.CompleteUpload(ctx, member, archive.ID, second.ID, false); err != nil {
		t.Fatal(err)
	}
	var submissions int
	owner.QueryRow(`SELECT count(*) FROM invoyis.submissions WHERE archive_id=$1`, archive.ID).Scan(&submissions)
	if submissions != 2 {
		t.Fatalf("identical sessions: %d", submissions)
	}
	third, err := s.CreateUpload(ctx, member, archive.ID, claim(data, "revoked"))
	if err != nil {
		t.Fatal(err)
	}
	storage.objects[third.Grant.Fields["key"]] = data
	if err = s.ChangeMember(ctx, admin, archive.ID, member, "viewer", false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteUpload(ctx, member, archive.ID, third.ID, false); err == nil {
		t.Fatal("demoted member completed upload")
	}
}
func TestUploadReceiptValidation(t *testing.T) {
	s, _, owner, admin := setup(t)
	_ = owner
	ctx := context.Background()
	storage := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = storage
	archive, err := s.Create(ctx, admin, "personal", "Validation", "")
	if err != nil {
		t.Fatal(err)
	}
	good := []byte("%PDF-1.0\nbroken internals")
	zero := claim(good, "zero")
	zero.ByteCount = 0
	if _, err := s.CreateUpload(ctx, admin, archive.ID, zero); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty declared input: %v", err)
	}
	tooLarge := claim(good, "oversized")
	tooLarge.ByteCount = (25 << 20) + 1
	if _, err := s.CreateUpload(ctx, admin, archive.ID, tooLarge); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized declared input: %v", err)
	}
	cases := [][]byte{nil, []byte("notpdf"), []byte("%PDF-1.0\nwrong size"), []byte("%PDF-1.0\nwrong digest")}
	for i, data := range cases {
		c := claim(good, fmt.Sprint(i))
		session, err := s.CreateUpload(ctx, admin, archive.ID, c)
		if err != nil {
			t.Fatal(err)
		}
		storage.objects[session.Grant.Fields["key"]] = data
		if _, err = s.CompleteUpload(ctx, admin, archive.ID, session.ID, false); err == nil {
			t.Fatalf("invalid case %d accepted", i)
		}
	}
	session, err := s.CreateUpload(ctx, admin, archive.ID, claim(good, "broken-structure"))
	if err != nil {
		t.Fatal(err)
	}
	storage.objects[session.Grant.Fields["key"]] = good
	if _, err = s.CompleteUpload(ctx, admin, archive.ID, session.ID, false); err != nil {
		t.Fatalf("PDF-shaped file should be receivable: %v", err)
	}
}

func TestUploadConcurrentCompletion(t *testing.T) {
	s, _, _, admin := setup(t)
	ctx := context.Background()
	storage := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = storage
	archive, err := s.Create(ctx, admin, "personal", "Concurrent", "")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("%PDF-1.0\nconcurrent synthetic")
	session, err := s.CreateUpload(ctx, admin, archive.ID, claim(data, "race"))
	if err != nil {
		t.Fatal(err)
	}
	storage.objects[session.Grant.Fields["key"]] = data
	const n = 8
	receipts := make(chan UploadReceipt, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.CompleteUpload(ctx, admin, archive.ID, session.ID, false)
			if e != nil {
				errs <- e
			} else {
				receipts <- r
			}
		}()
	}
	wg.Wait()
	close(receipts)
	close(errs)
	for e := range errs {
		t.Errorf("concurrent completion: %v", e)
	}
	first := ""
	count := 0
	for r := range receipts {
		count++
		if first == "" {
			first = r.ID
		} else if first != r.ID {
			t.Fatalf("different receipts %s %s", first, r.ID)
		}
	}
	if count != n {
		t.Fatalf("successful completions %d/%d", count, n)
	}
}

func TestUploadReconcileRevocationFlagsOrphan(t *testing.T) {
	s, _, owner, admin := setup(t)
	ctx := context.Background()
	storage := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = storage
	archive, err := s.Create(ctx, admin, "personal", "Revoked", "")
	if err != nil {
		t.Fatal(err)
	}
	contributor := addAccount(t, owner, "revoke@example.test")
	if _, err = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'contributor')`, archive.ID, contributor); err != nil {
		t.Fatal(err)
	}
	data := []byte("%PDF-1.0\nrevoked synthetic")
	session, err := s.CreateUpload(ctx, contributor, archive.ID, claim(data, "revoked-after-write"))
	if err != nil {
		t.Fatal(err)
	}
	storage.objects[session.Grant.Fields["key"]] = data
	s.AfterFinalWrite = func() error { return errors.New("synthetic crash") }
	if _, err = s.CompleteUpload(ctx, contributor, archive.ID, session.ID, false); err == nil {
		t.Fatal("expected interruption")
	}
	s.AfterFinalWrite = nil
	if err = s.ChangeMember(ctx, admin, archive.ID, contributor, "viewer", false); err != nil {
		t.Fatal(err)
	}
	owner.Exec(`UPDATE invoyis.upload_sessions SET finalising_at=now()-interval '1 minute' WHERE id=$1`, session.ID)
	discovery, err := sql.Open("pgx", os.Getenv("UPLOAD_RECONCILER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.Close()
	done, err := s.ReconcileUploads(ctx, discovery)
	if err != nil || done != 0 {
		t.Fatalf("reconcile revoked: %d %v", done, err)
	}
	var orphan string
	err = owner.QueryRow(`SELECT orphan_reason FROM invoyis.upload_sessions WHERE id=$1`, session.ID).Scan(&orphan)
	if err != nil || orphan != "receipt_blocked" {
		t.Fatalf("orphan flag: %q %v", orphan, err)
	}
	var count int
	owner.QueryRow(`SELECT count(*) FROM invoyis.upload_receipts WHERE archive_id=$1`, archive.ID).Scan(&count)
	if count != 0 {
		t.Fatal("revoked upload got receipt")
	}
}

func TestUploadBrowserReconcilerRace(t *testing.T) {
	s, _, owner, admin := setup(t)
	ctx := context.Background()
	storage := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = storage
	archive, err := s.Create(ctx, admin, "personal", "Race reconciliation", "")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("%PDF-1.0\nrace synthetic")
	c := claim(data, "race-with-reconciler")
	session, err := s.CreateUpload(ctx, admin, archive.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	var finalKey string
	err = owner.QueryRow(`SELECT final_key FROM invoyis.upload_sessions WHERE id=$1`, session.ID).Scan(&finalKey)
	if err != nil {
		t.Fatal(err)
	}
	storage.objects[finalKey] = data
	_, err = owner.Exec(`UPDATE invoyis.upload_sessions SET state='finalising',verified_bytes=$2,verified_sha256=$3,finalising_at=now()-interval '1 minute' WHERE id=$1`, session.ID, len(data), c.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := sql.Open("pgx", os.Getenv("UPLOAD_RECONCILER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	var browser UploadReceipt
	var browserErr, reconcileErr error
	go func() {
		defer wg.Done()
		browser, browserErr = s.CompleteUpload(ctx, admin, archive.ID, session.ID, false)
	}()
	go func() { defer wg.Done(); _, reconcileErr = s.ReconcileUploads(ctx, discovery) }()
	wg.Wait()
	if browserErr != nil || reconcileErr != nil {
		t.Fatalf("race browser=%v reconcile=%v", browserErr, reconcileErr)
	}
	again, err := s.CompleteUpload(ctx, admin, archive.ID, session.ID, false)
	if err != nil || again.ID != browser.ID {
		t.Fatalf("unstable receipt after race: %v", err)
	}
	var count int
	owner.QueryRow(`SELECT count(*) FROM invoyis.upload_receipts WHERE upload_session_id=$1`, session.ID).Scan(&count)
	if count != 1 {
		t.Fatalf("receipt count %d", count)
	}
}

func TestUploadRecoveryBeforeFinalWrite(t *testing.T) {
	s, _, owner, admin := setup(t)
	ctx := context.Background()
	storage := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = storage
	archive, err := s.Create(ctx, admin, "personal", "Before write", "")
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("%PDF-1.7\noriginal with synthetic XML <x/>")
	session, err := s.CreateUpload(ctx, admin, archive.ID, claim(original, "pre-write"))
	if err != nil {
		t.Fatal(err)
	}
	storage.objects[session.Grant.Fields["key"]] = original
	s.BeforeFinalWrite = func() error { return errors.New("simulated process stop before final PUT") }
	if _, err = s.CompleteUpload(ctx, admin, archive.ID, session.ID, false); err == nil {
		t.Fatal("expected interruption")
	}
	var finalKey, preparedKey, state string
	if err = owner.QueryRow(`SELECT final_key,prepared_key,state FROM invoyis.upload_sessions WHERE id=$1`, session.ID).Scan(&finalKey, &preparedKey, &state); err != nil {
		t.Fatal(err)
	}
	if state != "finalising" {
		t.Fatalf("state=%s", state)
	}
	storage.mu.Lock()
	_, finalExists := storage.objects[finalKey]
	prepared := bytes.Clone(storage.objects[preparedKey])
	storage.objects[session.Grant.Fields["key"]] = []byte("%PDF-1.7\nreplayed staging grant")
	storage.mu.Unlock()
	if finalExists || !bytes.Equal(prepared, original) {
		t.Fatal("prepared recovery object missing or final written too early")
	}
	restarted, err := New(os.Getenv("TEST_ARCHIVE_DATABASE_URL"), s.Auth, s.Sender, s.BrowserOrigin)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.Storage = storage
	if _, err = owner.Exec(`UPDATE invoyis.upload_sessions SET finalising_at=now()-interval '1 minute' WHERE id=$1`, session.ID); err != nil {
		t.Fatal(err)
	}
	discovery, err := sql.Open("pgx", os.Getenv("UPLOAD_RECONCILER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.Close()
	done, err := restarted.ReconcileUploads(ctx, discovery)
	if err != nil || done != 1 {
		t.Fatalf("reconcile before-write crash: %d %v", done, err)
	}
	stored, err := storage.Read(ctx, finalKey, 25<<20)
	if err != nil || !bytes.Equal(stored, original) {
		t.Fatalf("recovered bytes differ: %v", err)
	}
}
func TestUploadGrantRenewal(t *testing.T) {
	s, _, owner, admin := setup(t)
	_ = owner
	ctx := context.Background()
	s.Storage = &fakeOriginals{objects: map[string][]byte{}}
	archive, err := s.Create(ctx, admin, "personal", "Grant renewal", "")
	if err != nil {
		t.Fatal(err)
	}
	claim := claim([]byte("%PDF-1.0\nrenewal"), "renew")
	first, err := s.CreateUpload(ctx, admin, archive.ID, claim)
	if err != nil {
		t.Fatal(err)
	}
	originalExpiry, _ := time.Parse(time.RFC3339, first.Grant.ExpiresAt)
	s.Now = func() time.Time { return originalExpiry.Add(time.Second) }
	renewed, err := s.CreateUpload(ctx, admin, archive.ID, claim)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ID != first.ID || renewed.Grant.Fields["key"] != first.Grant.Fields["key"] {
		t.Fatal("renewal changed session or key")
	}
	newExpiry, _ := time.Parse(time.RFC3339, renewed.Grant.ExpiresAt)
	if !newExpiry.After(originalExpiry) {
		t.Fatal("grant was not renewed")
	}
}

type failingReadStorage struct {
	base *fakeOriginals
	key  string
}

func (f *failingReadStorage) Grant(ctx context.Context, key string, expiry time.Time, cap int64) (UploadGrant, error) {
	return f.base.Grant(ctx, key, expiry, cap)
}
func (f *failingReadStorage) Read(ctx context.Context, key string, cap int64) ([]byte, error) {
	if key == f.key {
		return nil, errors.New("synthetic transient storage outage")
	}
	return f.base.Read(ctx, key, cap)
}
func (f *failingReadStorage) PutOriginal(ctx context.Context, key string, data []byte) error {
	return f.base.PutOriginal(ctx, key, data)
}
func TestUploadReconcilerSkipsTransientSession(t *testing.T) {
	s, _, owner, admin := setup(t)
	ctx := context.Background()
	base := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = base
	archive, err := s.Create(ctx, admin, "personal", "Reconciler progress", "")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("%PDF-1.0\nsynthetic")
	bad, err := s.CreateUpload(ctx, admin, archive.ID, claim(data, "bad-storage"))
	if err != nil {
		t.Fatal(err)
	}
	good, err := s.CreateUpload(ctx, admin, archive.ID, claim(data, "good-storage"))
	if err != nil {
		t.Fatal(err)
	}
	var badKey, goodKey string
	for _, item := range []struct {
		id  string
		key *string
		age string
	}{{bad.ID, &badKey, "2 minutes"}, {good.ID, &goodKey, "1 minute"}} {
		if err = owner.QueryRow(`SELECT final_key FROM invoyis.upload_sessions WHERE id=$1`, item.id).Scan(item.key); err != nil {
			t.Fatal(err)
		}
		if _, err = owner.Exec(`UPDATE invoyis.upload_sessions SET state='finalising',verified_bytes=$2,verified_sha256=$3,finalising_at=now()-$4::interval WHERE id=$1`, item.id, len(data), claim(data, "unused").SHA256, item.age); err != nil {
			t.Fatal(err)
		}
		base.objects[*item.key] = bytes.Clone(data)
	}
	s.Storage = &failingReadStorage{base: base, key: badKey}
	discovery, err := sql.Open("pgx", os.Getenv("UPLOAD_RECONCILER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.Close()
	done, err := s.ReconcileUploads(ctx, discovery)
	if err != nil || done != 1 {
		t.Fatalf("one bad session blocked next: %d %v", done, err)
	}
	var attempts int
	var next time.Time
	if err = owner.QueryRow(`SELECT reconcile_attempts,next_reconcile_at FROM invoyis.upload_sessions WHERE id=$1`, bad.ID).Scan(&attempts, &next); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || !next.After(time.Now()) {
		t.Fatalf("no backoff: %d %v", attempts, next)
	}
}
