package processing

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/archive"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type memoryStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
	failure error
}

func (s *memoryStorage) Read(_ context.Context, key string, cap int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return nil, s.failure
	}
	data, ok := s.objects[key]
	if !ok {
		return nil, errors.New("missing")
	}
	if int64(len(data)) > cap {
		return nil, errors.New("oversize")
	}
	return bytes.Clone(data), nil
}
func seedJob(t *testing.T, owner *sql.DB, data []byte) string {
	t.Helper()
	ctx := context.Background()
	var account int64
	if err := owner.QueryRowContext(ctx, `INSERT INTO invoyis.accounts(email) VALUES('issue7-'||gen_random_uuid()||'@example.test') RETURNING id`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	var archive, session, object, submission, document, job string
	if err := owner.QueryRowContext(ctx, `INSERT INTO invoyis.archives(id,kind,name,created_by) VALUES(gen_random_uuid(),'personal','Issue 7 synthetic', $1) RETURNING id`, account).Scan(&archive); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	sha := hex.EncodeToString(digest[:])
	key := "originals/" + archive + "/synthetic"
	if err := owner.QueryRowContext(ctx, `INSERT INTO invoyis.upload_sessions(id,archive_id,uploader_id,idempotency_key,filename,media_type,declared_bytes,declared_sha256,staging_key,prepared_key,final_key,state,grant_expires_at,finalise_by)
 VALUES(gen_random_uuid(),$1::uuid,$2,'synthetic','synthetic.pdf','application/pdf',$3,$4,'staging/'||($1::uuid)::text||'/synthetic','prepared/'||($1::uuid)::text||'/synthetic',$5,'complete',now()+interval '1 hour',now()+interval '1 day') RETURNING id`, archive, account, len(data), sha, key).Scan(&session); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRowContext(ctx, `INSERT INTO invoyis.stored_objects(id,archive_id,upload_session_id,storage_key,byte_count,sha256) VALUES(gen_random_uuid(),$1,$2,$3,$4,$5) RETURNING id`, archive, session, key, len(data), sha).Scan(&object); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRowContext(ctx, `INSERT INTO invoyis.submissions(id,archive_id,upload_session_id,stored_object_id,uploader_id,source,original_filename) VALUES(gen_random_uuid(),$1,$2,$3,$4,'browser_upload','synthetic.pdf') RETURNING id`, archive, session, object, account).Scan(&submission); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRowContext(ctx, `INSERT INTO invoyis.documents(id,archive_id,submission_id,state) VALUES(gen_random_uuid(),$1,$2,'received') RETURNING id`, archive, submission).Scan(&document); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRowContext(ctx, `INSERT INTO invoyis.processing_jobs(id,archive_id,document_id,state) VALUES(gen_random_uuid(),$1,$2,'pending') RETURNING id`, archive, document).Scan(&job); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		owner.Exec(`DELETE FROM invoyis.extraction_runs WHERE document_id=$1`, document)
		owner.Exec(`DELETE FROM invoyis.processing_jobs WHERE id=$1`, job)
		owner.Exec(`DELETE FROM invoyis.documents WHERE id=$1`, document)
		owner.Exec(`DELETE FROM invoyis.submissions WHERE id=$1`, submission)
		owner.Exec(`DELETE FROM invoyis.stored_objects WHERE id=$1`, object)
		owner.Exec(`DELETE FROM invoyis.upload_sessions WHERE id=$1`, session)
		owner.Exec(`DELETE FROM invoyis.archives WHERE id=$1`, archive)
		owner.Exec(`DELETE FROM invoyis.accounts WHERE id=$1`, account)
	})
	return key
}
func TestLeaseLifecycle(t *testing.T) {
	ownerURL, workerURL := os.Getenv("PROCESSING_TEST_DATABASE_URL"), os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL")
	if ownerURL == "" || workerURL == "" {
		t.Skip("disposable PostgreSQL URLs required")
	}
	owner, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	data := []byte("%PDF-1.7\nsynthetic\n%%EOF")
	key := seedJob(t, owner, data)
	storage := &memoryStorage{objects: map[string][]byte{key: data}}
	parse := func(context.Context, []byte) (Result, error) {
		return Result{Profile: "basic", Candidates: map[string]any{"invoiceId": "00012", "total": "1.00"}, Provenance: map[string]any{"invoiceId": "/ID"}, Warnings: []string{}}, nil
	}
	config := DefaultConfig()
	config.Lease = 3 * time.Second
	config.Deadline = 10 * time.Second
	first, err := Open(workerURL, storage, func(context.Context, []byte) (bool, error) { return true, nil }, parse, config)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(workerURL, storage, func(context.Context, []byte) (bool, error) { return true, nil }, parse, config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(2)
	claims := make(chan *Claim, 2)
	for _, engine := range []*Engine{first, second} {
		go func(e *Engine) {
			defer wg.Done()
			c, err := e.Claim(ctx)
			if err != nil {
				t.Error(err)
			}
			claims <- c
		}(engine)
	}
	wg.Wait()
	close(claims)
	var winner *Claim
	count := 0
	for c := range claims {
		if c != nil {
			winner = c
			count++
		}
	}
	if count != 1 {
		t.Fatalf("claims=%d", count)
	}
	if err := first.Process(ctx, *winner); err != nil {
		t.Fatal(err)
	}
	var state, docState, clearance string
	var runs int
	err = owner.QueryRow(`SELECT j.state,d.state,d.clearance,(SELECT count(*) FROM invoyis.extraction_runs WHERE document_id=d.id) FROM invoyis.processing_jobs j JOIN invoyis.documents d ON d.id=j.document_id WHERE j.id=$1`, winner.JobID).Scan(&state, &docState, &clearance, &runs)
	if err != nil || state != "succeeded" || docState != "needs_review" || clearance != "cleared" || runs != 1 {
		t.Fatalf("state=%s document=%s clearance=%s runs=%d err=%v", state, docState, clearance, runs, err)
	}
	if ok, err := second.Heartbeat(ctx, *winner); err != nil || ok {
		t.Fatalf("stale heartbeat=%t err=%v", ok, err)
	}
}

func TestExpiredLeaseRecoveryAndBudgets(t *testing.T) {
	ownerURL, workerURL := os.Getenv("PROCESSING_TEST_DATABASE_URL"), os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL")
	if ownerURL == "" || workerURL == "" {
		t.Skip("disposable PostgreSQL URLs required")
	}
	owner, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	data := []byte("%PDF-1.7\nsynthetic recovery\n%%EOF")
	storage := &memoryStorage{objects: map[string][]byte{}}
	parser := func(context.Context, []byte) (Result, error) {
		return Result{Candidates: map[string]any{}, Provenance: map[string]any{}, Warnings: []string{"no_supported_xml"}}, nil
	}
	config := DefaultConfig()
	config.Lease = 3 * time.Second
	config.Deadline = 10 * time.Second
	first, err := Open(workerURL, storage, func(context.Context, []byte) (bool, error) { return true, nil }, parser, config)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(workerURL, storage, func(context.Context, []byte) (bool, error) { return true, nil }, parser, config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx := context.Background()
	key := seedJob(t, owner, data)
	storage.objects[key] = data
	stale, err := first.Claim(ctx)
	if err != nil || stale == nil {
		t.Fatalf("claim %v %v", stale, err)
	}
	var originalExpiry, timeAfter time.Time
	if err = owner.QueryRow(`SELECT lease_expires_at FROM invoyis.processing_jobs WHERE id=$1`, stale.JobID).Scan(&originalExpiry); err != nil {
		t.Fatal(err)
	}
	if ok, err := first.Heartbeat(ctx, *stale); err != nil || !ok {
		t.Fatalf("heartbeat=%t %v", ok, err)
	}
	if err = owner.QueryRow(`SELECT lease_expires_at FROM invoyis.processing_jobs WHERE id=$1`, stale.JobID).Scan(&timeAfter); err != nil || timeAfter.Before(originalExpiry) {
		t.Fatalf("heartbeat expiry %v %v", timeAfter, err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.processing_jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, stale.JobID); err != nil {
		t.Fatal(err)
	}
	if ok, err := first.Heartbeat(ctx, *stale); err != nil || ok {
		t.Fatalf("expired heartbeat=%t %v", ok, err)
	}
	if err = second.Reap(ctx); err != nil {
		t.Fatal(err)
	}
	winner, err := second.Claim(ctx)
	if err != nil || winner == nil || winner.Token == stale.Token {
		t.Fatalf("replacement %v %v", winner, err)
	}
	if err = second.Process(ctx, *winner); err != nil {
		t.Fatal(err)
	}
	if ok, err := first.boolCall(ctx, `SELECT invoyis.worker_complete($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8::jsonb)`, stale.JobID, stale.Token, stale.SHA, ExtractorVersion, "", `{}`, `{}`, `[]`); err != nil || ok {
		t.Fatalf("stale completion=%t %v", ok, err)
	}
	var runs int
	if err = owner.QueryRow(`SELECT count(*) FROM invoyis.extraction_runs WHERE document_id=$1`, winner.DocumentID).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("runs=%d %v", runs, err)
	}

	finalKey := seedJob(t, owner, data)
	storage.objects[finalKey] = data
	var finalID string
	if err = owner.QueryRow(`UPDATE invoyis.processing_jobs SET attempts=4 WHERE document_id=(SELECT id FROM invoyis.documents WHERE submission_id=(SELECT id FROM invoyis.submissions WHERE stored_object_id=(SELECT id FROM invoyis.stored_objects WHERE storage_key=$1))) RETURNING id`, finalKey).Scan(&finalID); err != nil {
		t.Fatal(err)
	}
	final, err := first.Claim(ctx)
	if err != nil || final == nil || final.JobID != finalID || final.Attempts != 5 {
		t.Fatalf("final claim %v %v", final, err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.processing_jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, final.JobID); err != nil {
		t.Fatal(err)
	}
	if err = second.Reap(ctx); err != nil {
		t.Fatal(err)
	}
	var state, code string
	if err = owner.QueryRow(`SELECT state,failure_code FROM invoyis.processing_jobs WHERE id=$1`, final.JobID).Scan(&state, &code); err != nil || state != "failed" || code != "lease_expired_exhausted" {
		t.Fatalf("final state %s %s %v", state, code, err)
	}
	next, err := second.Claim(ctx)
	if err != nil || next != nil {
		t.Fatalf("exhausted claim %v %v", next, err)
	}

	parserKey := seedJob(t, owner, data)
	storage.objects[parserKey] = data
	parserClaim, err := first.Claim(ctx)
	if err != nil || parserClaim == nil {
		t.Fatalf("parser claim %v %v", parserClaim, err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.processing_jobs SET parser_attempts=2,lease_expires_at=now()-interval '1 second' WHERE id=$1`, parserClaim.JobID); err != nil {
		t.Fatal(err)
	}
	if err = second.Reap(ctx); err != nil {
		t.Fatal(err)
	}
	if err = owner.QueryRow(`SELECT state,failure_code FROM invoyis.processing_jobs WHERE id=$1`, parserClaim.JobID).Scan(&state, &code); err != nil || state != "failed" || code != "parser_attempts_exhausted" {
		t.Fatalf("parser state %s %s %v", state, code, err)
	}
}

func TestProcessKillRecovery(t *testing.T) {
	ownerURL, workerURL := os.Getenv("PROCESSING_TEST_DATABASE_URL"), os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL")
	if ownerURL == "" || workerURL == "" {
		t.Skip("disposable PostgreSQL URLs required")
	}
	if mode := os.Getenv("ISSUE7_CHILD_MODE"); mode != "" {
		data := []byte("%PDF-1.7\nsynthetic killed worker\n%%EOF")
		storage := &memoryStorage{objects: map[string][]byte{os.Getenv("ISSUE7_CHILD_KEY"): data}}
		config := DefaultConfig()
		config.Lease = 3 * time.Second
		config.Deadline = 30 * time.Second
		parser := func(context.Context, []byte) (Result, error) {
			return Result{Candidates: map[string]any{"invoiceId": "00077"}, Provenance: map[string]any{"invoiceId": "/ID"}, Warnings: []string{}}, nil
		}
		engine, err := Open(workerURL, storage, func(context.Context, []byte) (bool, error) { return true, nil }, parser, config)
		if err != nil {
			panic(err)
		}
		claim, err := engine.Claim(context.Background())
		if err != nil || claim == nil {
			panic(fmt.Sprintf("claim %v %v", claim, err))
		}
		encoded, _ := json.Marshal(claim)
		fmt.Println(string(encoded))
		if mode == "after_claim" {
			time.Sleep(time.Hour)
		}
		engine.BeforeCommit = func() error { fmt.Println("extracted"); time.Sleep(time.Hour); return nil }
		_ = engine.Process(context.Background(), *claim)
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("SIGKILL unavailable")
	}
	owner, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	data := []byte("%PDF-1.7\nsynthetic killed worker\n%%EOF")
	for _, mode := range []string{"after_claim", "after_extraction"} {
		t.Run(mode, func(t *testing.T) {
			key := seedJob(t, owner, data)
			command := exec.Command(os.Args[0], "-test.run=^TestProcessKillRecovery$")
			command.Env = append(os.Environ(), "ISSUE7_CHILD_MODE="+mode, "ISSUE7_CHILD_KEY="+key)
			stdout, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(stdout)
			if !scanner.Scan() {
				t.Fatalf("child exited before claim: %v", scanner.Err())
			}
			var stale Claim
			if err = json.Unmarshal(scanner.Bytes(), &stale); err != nil {
				t.Fatal(err)
			}
			if mode == "after_extraction" {
				if !scanner.Scan() || scanner.Text() != "extracted" {
					t.Fatal("child did not reach precommit hook")
				}
			}
			if err = command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			if _, err = owner.Exec(`UPDATE invoyis.processing_jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, stale.JobID); err != nil {
				t.Fatal(err)
			}
			storage := &memoryStorage{objects: map[string][]byte{key: data}}
			parser := func(context.Context, []byte) (Result, error) {
				return Result{Candidates: map[string]any{"invoiceId": "00077"}, Provenance: map[string]any{"invoiceId": "/ID"}, Warnings: []string{}}, nil
			}
			config := DefaultConfig()
			config.Lease = 3 * time.Second
			config.Deadline = 10 * time.Second
			replacement, err := Open(workerURL, storage, func(context.Context, []byte) (bool, error) { return true, nil }, parser, config)
			if err != nil {
				t.Fatal(err)
			}
			defer replacement.Close()
			if err = replacement.Reap(context.Background()); err != nil {
				t.Fatal(err)
			}
			winning, err := replacement.Claim(context.Background())
			if err != nil || winning == nil || winning.Token == stale.Token {
				t.Fatalf("replacement %v %v", winning, err)
			}
			if err = replacement.Process(context.Background(), *winning); err != nil {
				t.Fatal(err)
			}
			if ok, err := replacement.boolCall(context.Background(), `SELECT invoyis.worker_complete($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8::jsonb)`, stale.JobID, stale.Token, stale.SHA, ExtractorVersion, "basic", `{}`, `{}`, `[]`); err != nil || ok {
				t.Fatalf("stale completion %t %v", ok, err)
			}
			var runs int
			if err = owner.QueryRow(`SELECT count(*) FROM invoyis.extraction_runs WHERE document_id=$1`, winning.DocumentID).Scan(&runs); err != nil || runs != 1 {
				t.Fatalf("run count %d %v", runs, err)
			}
		})
	}
}

func TestUpgradeFromReceivedJob(t *testing.T) {
	ownerURL, workerURL, fixturePath := os.Getenv("PROCESSING_TEST_DATABASE_URL"), os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL"), os.Getenv("PROCESSING_UPGRADE_FIXTURE_PATH")
	if ownerURL == "" || workerURL == "" || fixturePath == "" {
		t.Skip("pre-migration synthetic fixture is required")
	}
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	var key, receiptDigest, receiptID string
	var documentID string
	var receiptBytes int64
	err = owner.QueryRow(`SELECT o.storage_key,r.sha256,r.byte_count,r.id,d.id FROM invoyis.processing_jobs j
 JOIN invoyis.documents d ON d.id=j.document_id JOIN invoyis.submissions s ON s.id=d.submission_id
 JOIN invoyis.stored_objects o ON o.id=s.stored_object_id JOIN invoyis.upload_receipts r ON r.document_id=d.id
 WHERE j.state='pending' ORDER BY j.created_at LIMIT 1`).Scan(&key, &receiptDigest, &receiptBytes, &receiptID, &documentID)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if receiptDigest != hex.EncodeToString(digest[:]) || receiptBytes != int64(len(data)) {
		t.Fatal("pre-migration receipt differs from original")
	}
	storage := &memoryStorage{objects: map[string][]byte{key: data}}
	parser := func(context.Context, []byte) (Result, error) {
		return Result{Profile: "basic", Candidates: map[string]any{"invoiceId": "00014"}, Provenance: map[string]any{"invoiceId": "/ID"}, Warnings: []string{}}, nil
	}
	config := DefaultConfig()
	engine, err := Open(workerURL, storage, func(context.Context, []byte) (bool, error) { return true, nil }, parser, config)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	claim, err := engine.Claim(context.Background())
	if err != nil || claim == nil {
		t.Fatalf("claim %v %v", claim, err)
	}
	if err = engine.Process(context.Background(), *claim); err != nil {
		t.Fatal(err)
	}
	var state string
	var checkDigest string
	var checkBytes int64
	var checkReceipt string
	err = owner.QueryRow(`SELECT d.state,r.sha256,r.byte_count,r.id FROM invoyis.documents d JOIN invoyis.upload_receipts r ON r.document_id=d.id WHERE d.id=$1`, documentID).Scan(&state, &checkDigest, &checkBytes, &checkReceipt)
	if err != nil || state != "needs_review" || checkDigest != receiptDigest || checkBytes != receiptBytes || checkReceipt != receiptID {
		t.Fatalf("upgrade state %s receipt %s %d %s %v", state, checkDigest, checkBytes, checkReceipt, err)
	}
}

func TestWorkerFailurePolicy(t *testing.T) {
	ownerURL, workerURL := os.Getenv("PROCESSING_TEST_DATABASE_URL"), os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL")
	if ownerURL == "" || workerURL == "" {
		t.Skip("disposable PostgreSQL URLs required")
	}
	owner, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	data := []byte("%PDF-1.7\nsynthetic failure\n%%EOF")
	cases := []struct {
		name                string
		storageErr          error
		tamper              bool
		scan                Scanner
		wantState, wantCode string
	}{
		{"storage outage", errors.New("offline"), false, func(context.Context, []byte) (bool, error) { return true, nil }, "pending", ""},
		{"scanner outage", nil, false, func(context.Context, []byte) (bool, error) { return false, errors.New("offline") }, "pending", ""},
		{"positive scan", nil, false, func(context.Context, []byte) (bool, error) { return false, nil }, "failed", "scan_rejected"},
		{"original mismatch", nil, true, func(context.Context, []byte) (bool, error) { return true, nil }, "failed", "original_integrity_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := seedJob(t, owner, data)
			stored := bytes.Clone(data)
			if tc.tamper {
				stored = append(stored, 'x')
			}
			storage := &memoryStorage{objects: map[string][]byte{key: stored}, failure: tc.storageErr}
			config := DefaultConfig()
			config.Lease = 3 * time.Second
			config.Deadline = 10 * time.Second
			engine, err := Open(workerURL, storage, tc.scan, func(context.Context, []byte) (Result, error) { t.Fatal("parser must not run"); return Result{}, nil }, config)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			claim, err := engine.Claim(context.Background())
			if err != nil || claim == nil {
				t.Fatalf("claim %v %v", claim, err)
			}
			if err = engine.Process(context.Background(), *claim); err != nil {
				t.Fatal(err)
			}
			var state string
			var code sql.NullString
			if err = owner.QueryRow(`SELECT state,failure_code FROM invoyis.processing_jobs WHERE id=$1`, claim.JobID).Scan(&state, &code); err != nil {
				t.Fatal(err)
			}
			if state != tc.wantState || code.String != tc.wantCode {
				t.Fatalf("state %s code %s", state, code.String)
			}
		})
	}
}

func TestRealScannerWorkerPolicy(t *testing.T) {
	ownerURL, workerURL := os.Getenv("PROCESSING_TEST_DATABASE_URL"), os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL")
	if ownerURL == "" || workerURL == "" {
		t.Skip("disposable PostgreSQL URLs required")
	}
	dir := configureRealScanner(t)
	owner, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	for _, tc := range []struct {
		name, binary, wantJob, wantCode, wantClearance string
	}{
		{"EICAR detection", "", "failed", "scan_rejected", "blocked"},
		{"scanner missing", filepath.Join(dir, "missing-scanner"), "pending", "", "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAMSCAN_BINARY", tc.binary)
			key := seedJob(t, owner, eicarBytes)
			storage := &memoryStorage{objects: map[string][]byte{key: eicarBytes}}
			engine, err := Open(workerURL, storage, ClamScan, func(context.Context, []byte) (Result, error) {
				t.Fatal("rejected or unscanned content reached parser")
				return Result{}, nil
			}, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			claim, err := engine.Claim(context.Background())
			if err != nil || claim == nil {
				t.Fatalf("claim=%v err=%v", claim, err)
			}
			if err := engine.Process(context.Background(), *claim); err != nil {
				t.Fatal(err)
			}
			var job, clearance string
			var code sql.NullString
			if err := owner.QueryRow(`SELECT j.state,j.failure_code,d.clearance FROM invoyis.processing_jobs j JOIN invoyis.documents d ON d.id=j.document_id WHERE j.id=$1`, claim.JobID).Scan(&job, &code, &clearance); err != nil {
				t.Fatal(err)
			}
			if job != tc.wantJob || code.String != tc.wantCode || clearance != tc.wantClearance {
				t.Fatalf("job=%s code=%s clearance=%s", job, code.String, clearance)
			}
		})
	}
}

func TestTwoWorkersDrainQueue(t *testing.T) {
	ownerURL, workerURL := os.Getenv("PROCESSING_TEST_DATABASE_URL"), os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL")
	if ownerURL == "" || workerURL == "" {
		t.Skip("disposable PostgreSQL URLs required")
	}
	owner, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	data := []byte("%PDF-1.7\nsynthetic concurrent queue\n%%EOF")
	storage := &memoryStorage{objects: map[string][]byte{}}
	for range 4 {
		key := seedJob(t, owner, data)
		storage.objects[key] = data
	}
	parser := func(context.Context, []byte) (Result, error) {
		return Result{Profile: "basic", Candidates: map[string]any{"invoiceId": "0001"}, Provenance: map[string]any{"invoiceId": "/ID"}, Warnings: []string{}}, nil
	}
	config := DefaultConfig()
	config.Lease = 3 * time.Second
	config.Deadline = 10 * time.Second
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			engine, err := Open(workerURL, storage, func(context.Context, []byte) (bool, error) { return true, nil }, parser, config)
			if err != nil {
				t.Error(err)
				return
			}
			defer engine.Close()
			for {
				claim, err := engine.Claim(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if claim == nil {
					return
				}
				if err = engine.Process(context.Background(), *claim); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	var succeeded, runs int
	if err = owner.QueryRow(`SELECT count(*) FROM invoyis.processing_jobs WHERE state='succeeded'`).Scan(&succeeded); err != nil {
		t.Fatal(err)
	}
	if err = owner.QueryRow(`SELECT count(*) FROM invoyis.extraction_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if succeeded < 4 || runs < 4 {
		t.Fatalf("succeeded=%d runs=%d", succeeded, runs)
	}
}

func TestParserAndStorageRetryExhaustion(t *testing.T) {
	ownerURL, workerURL := os.Getenv("PROCESSING_TEST_DATABASE_URL"), os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL")
	if ownerURL == "" || workerURL == "" {
		t.Skip("disposable PostgreSQL URLs required")
	}
	owner, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	data := []byte("%PDF-1.7\nsynthetic retry\n%%EOF")
	for _, mode := range []string{"parser", "storage"} {
		t.Run(mode, func(t *testing.T) {
			key := seedJob(t, owner, data)
			storage := &memoryStorage{objects: map[string][]byte{key: data}}
			parserStarts := 0
			parser := func(context.Context, []byte) (Result, error) {
				parserStarts++
				return Result{}, &ParseFailure{"parser_timeout", true}
			}
			config := DefaultConfig()
			config.Lease = 3 * time.Second
			config.Deadline = 10 * time.Second
			if mode == "storage" {
				config.MaxClaims = 2
				storage.failure = errors.New("storage offline")
			}
			engine, err := Open(workerURL, storage, func(context.Context, []byte) (bool, error) { return true, nil }, parser, config)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			ctx := context.Background()
			var job string
			for i := 0; i < 2; i++ {
				claim, err := engine.Claim(ctx)
				if err != nil || claim == nil {
					t.Fatalf("claim %d %v %v", i, claim, err)
				}
				job = claim.JobID
				if err = engine.Process(ctx, *claim); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					if _, err = owner.Exec(`UPDATE invoyis.processing_jobs SET next_run_at=now() WHERE id=$1`, job); err != nil {
						t.Fatal(err)
					}
				}
			}
			var state string
			var code sql.NullString
			var attempts, starts int
			if err = owner.QueryRow(`SELECT state,failure_code,attempts,parser_attempts FROM invoyis.processing_jobs WHERE id=$1`, job).Scan(&state, &code, &attempts, &starts); err != nil {
				t.Fatal(err)
			}
			want := "parser_attempts_exhausted"
			if mode == "storage" {
				want = "storage_unavailable"
			}
			if state != "failed" || code.String != want || attempts != 2 || starts != parserStarts {
				t.Fatalf("state=%s code=%s claims=%d parser starts=%d observed=%d", state, code.String, attempts, starts, parserStarts)
			}
			none, err := engine.Claim(ctx)
			if err != nil || none != nil {
				t.Fatalf("exhausted job claimed: %v %v", none, err)
			}
		})
	}
}

func TestSeaweedReadOnlyOriginalAndFacturX(t *testing.T) {
	if os.Getenv("PROCESSING_PROVIDER_TEST") != "1" {
		t.Skip("real provider opt-in")
	}
	ownerURL, workerURL, fixturePath := os.Getenv("PROCESSING_TEST_DATABASE_URL"), os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL"), os.Getenv("PROCESSING_PROVIDER_FIXTURE_PATH")
	if ownerURL == "" || workerURL == "" || fixturePath == "" || os.Getenv("PROCESSING_PARSER_BINARY") == "" {
		t.Fatal("provider test configuration missing")
	}
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	key := seedJob(t, owner, data)
	adminStorage, err := archive.StorageFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err = adminStorage.PutOriginal(context.Background(), key, data); err != nil {
		t.Fatal(err)
	}
	t.Setenv("S3_ACCESS_KEY_ID", os.Getenv("WORKER_S3_ACCESS_KEY_ID"))
	t.Setenv("S3_SECRET_ACCESS_KEY", os.Getenv("WORKER_S3_SECRET_ACCESS_KEY"))
	workerStorage, err := archive.StorageFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err = workerStorage.PutOriginal(context.Background(), key, data); err == nil {
		t.Fatal("worker credential overwrote original")
	}
	config := DefaultConfig()
	engine, err := Open(workerURL, workerStorage, func(context.Context, []byte) (bool, error) { return true, nil }, ParseIsolated, config)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	claim, err := engine.Claim(context.Background())
	if err != nil || claim == nil {
		t.Fatalf("claim %v %v", claim, err)
	}
	if err = engine.Process(context.Background(), *claim); err != nil {
		t.Fatal(err)
	}
	var state, profile, invoiceID string
	var runs int
	err = owner.QueryRow(`SELECT d.state,x.profile,x.candidates->>'invoiceId',(SELECT count(*) FROM invoyis.extraction_runs WHERE document_id=d.id)
 FROM invoyis.documents d JOIN invoyis.extraction_runs x ON x.document_id=d.id WHERE d.id=$1`, claim.DocumentID).Scan(&state, &profile, &invoiceID, &runs)
	if err != nil || state != "needs_review" || profile != "basic" || invoiceID != "0000123" || runs != 1 {
		t.Fatalf("state %s profile %s id %s runs %d err %v", state, profile, invoiceID, runs, err)
	}
	readback, err := workerStorage.Read(context.Background(), key, 25<<20)
	if err != nil || !bytes.Equal(data, readback) {
		t.Fatal("original changed after extraction")
	}
}
