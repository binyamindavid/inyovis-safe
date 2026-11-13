package archive

import (
	"context"
	"database/sql"
	"errors"
	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/retention"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func retentionFixture(t *testing.T) (*Service, *sql.DB, int64, string, string) {
	t.Helper()
	s, _, owner, actor := setup(t)
	if _, err := owner.Exec(`UPDATE invoyis.retention_active_rule_set SET version='empty-v1' WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	a, e := s.Create(context.Background(), actor, "business", "Retention synthetic", "FR")
	if e != nil {
		t.Fatal(e)
	}
	doc, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "retention")
	_, e = write(t, s, actor, a.ID, doc, "confirm", 0, ReviewMetadata{DocumentType: TypeLease, Title: "Synthetic lease", IssueDate: "2024-02-29"})
	if e != nil {
		t.Fatal(e)
	}
	return s, owner, actor, a.ID, doc
}
func checkCurrent(t *testing.T, s *Service, actor int64, a, d, want string) CurrentRetentionResult {
	t.Helper()
	r, e := s.CurrentRetention(context.Background(), actor, a, d)
	if e != nil || r.State != want {
		t.Fatalf("state=%+v err=%v want=%s", r, e, want)
	}
	return r
}
func drainRetention(t *testing.T, s *Service) {
	t.Helper()
	n, e := s.ReconcileRetention(context.Background(), 32)
	if e != nil || n < 1 {
		t.Fatalf("reconcile %d %v", n, e)
	}
}
func activateRules(t *testing.T, owner *sql.DB) {
	t.Helper()
	version, _ := newID()
	_, e := owner.Exec(`INSERT INTO invoyis.retention_rule_sets(version) VALUES($1)`, version)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range []struct {
		id, trigger string
		years       int
	}{{"year", "financial_year_end", 3}, {"filing", "filing_date", 5}} {
		_, e = owner.Exec(`INSERT INTO invoyis.retention_rule_versions(rule_id,version,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active) VALUES($1,1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','synthetic',$3,$4,true)`, version+r.id, version, r.trigger, r.years)
		if e != nil {
			t.Fatal(e)
		}
	}
	_, e = owner.Exec(`INSERT INTO invoyis.retention_coverage(coverage_id,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,status,source_reference) VALUES($1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','complete','synthetic')`, version+"coverage", version)
	if e != nil {
		t.Fatal(e)
	}
	_, e = owner.Exec(`UPDATE invoyis.retention_active_rule_set SET version=$1 WHERE singleton=true`, version)
	if e != nil {
		t.Fatal(e)
	}
}
func TestRetentionFlow(t *testing.T) {
	s, owner, actor, a, d := retentionFixture(t)
	ctx := context.Background()
	checkCurrent(t, s, actor, a, d, "missing")
	drainRetention(t, s)
	first := checkCurrent(t, s, actor, a, d, "current")
	if first.Snapshot.Outcome.Status != "pending" {
		t.Fatal(first)
	}
	_, e := write(t, s, actor, a, d, "draft", 1, ReviewMetadata{DocumentType: TypeLease, Title: "draft only", IssueDate: "2025-01-01"})
	if e != nil {
		t.Fatal(e)
	}
	if checkCurrent(t, s, actor, a, d, "current").Snapshot.ID != first.Snapshot.ID {
		t.Fatal("draft invalidated")
	}
	_, e = s.SetRetentionDocumentFacts(ctx, actor, a, d, 0, "company", 2024, "admin assigned")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetRetentionDocumentFacts(ctx, actor, a, d, 0, "other", 2024, "stale"); !errors.Is(e, ErrConflict) {
		t.Fatalf("stale assignment: %v", e)
	}
	checkCurrent(t, s, actor, a, d, "stale")
	activateRules(t, owner)
	checkCurrent(t, s, actor, a, d, "stale")
	drainRetention(t, s)
	end := &TaxYearEvent{Date: retention.Date("2024-02-29"), Source: "synthetic"}
	_, e = s.PutTaxYearFacts(ctx, actor, a, 2024, 0, end, nil)
	if e != nil {
		t.Fatal(e)
	}
	checkCurrent(t, s, actor, a, d, "stale")
	drainRetention(t, s)
	lower := checkCurrent(t, s, actor, a, d, "current")
	if lower.Snapshot.Outcome.LowerBound != "2027-02-28" || lower.Snapshot.Outcome.KeepThrough != "" {
		t.Fatal(lower)
	}
	filing := &TaxYearEvent{Date: retention.Date("2025-01-31"), Source: "synthetic"}
	corrected, e := s.PutTaxYearFacts(ctx, actor, a, 2024, 1, end, filing)
	if e != nil {
		t.Fatal(e)
	}
	if corrected.FinancialYearEnd.ActorID != actor || corrected.FinancialYearEnd.RecordedAt.IsZero() {
		t.Fatal("year-end provenance lost")
	}
	checkCurrent(t, s, actor, a, d, "stale")
	drainRetention(t, s)
	final := checkCurrent(t, s, actor, a, d, "current")
	if final.Snapshot.Outcome.KeepThrough != "2030-01-31" || final.Snapshot.ID == lower.Snapshot.ID {
		t.Fatal(final)
	}
	var oldFiling string
	if e = owner.QueryRow(`SELECT COALESCE(inputs->'events'->'filingDate'->>'date','') FROM invoyis.retention_evaluations WHERE id=$1`, lower.Snapshot.ID).Scan(&oldFiling); e != nil || oldFiling != "" {
		t.Fatalf("earlier evidence changed: %q %v", oldFiling, e)
	}
	var n int
	e = owner.QueryRow(`SELECT count(*) FROM invoyis.archive_tax_year_history WHERE archive_id=$1`, a).Scan(&n)
	if e != nil || n != 2 {
		t.Fatal(n, e)
	}
	var beforeRead int
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.retention_evaluations WHERE document_id=$1`, d).Scan(&beforeRead); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		checkCurrent(t, s, actor, a, d, "current")
	}
	var afterRead int
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.retention_evaluations WHERE document_id=$1`, d).Scan(&afterRead); e != nil || afterRead != beforeRead {
		t.Fatalf("read wrote snapshots %d -> %d: %v", beforeRead, afterRead, e)
	}
	_, e = write(t, s, actor, a, d, "confirm", 2, ReviewMetadata{DocumentType: TypeLease, Title: "Corrected lease", IssueDate: "2024-03-01"})
	if e != nil {
		t.Fatal(e)
	}
	checkCurrent(t, s, actor, a, d, "stale")
	drainRetention(t, s)
	if got := checkCurrent(t, s, actor, a, d, "current"); got.Snapshot.ConfirmedRevisionID == final.Snapshot.ConfirmedRevisionID {
		t.Fatal("confirmed correction reused old revision")
	}
	_, e = s.PutTaxYearFacts(ctx, actor, a, 2024, 1, end, filing)
	if !errors.Is(e, ErrConflict) {
		t.Fatal("stale write", e)
	}
	_, e = s.PutTaxYearFacts(ctx, actor, a, 2024, 2, end, filing)
	if e != nil {
		t.Fatal(e)
	}
	_, e = owner.Exec(`UPDATE invoyis.documents SET clearance='blocked' WHERE id=$1`, d)
	if e != nil {
		t.Fatal(e)
	}
	checkCurrent(t, s, actor, a, d, "unavailable")
	if _, e = owner.Exec(`UPDATE invoyis.documents SET clearance='cleared' WHERE id=$1`, d); e != nil {
		t.Fatal(e)
	}
	checkCurrent(t, s, actor, a, d, "current")
	if _, e = owner.Exec(`UPDATE invoyis.archives SET jurisdiction='IE' WHERE id=$1`, a); e != nil {
		t.Fatal(e)
	}
	checkCurrent(t, s, actor, a, d, "stale")
	drainRetention(t, s)
	if got := checkCurrent(t, s, actor, a, d, "current"); got.Snapshot.Outcome.KeepThrough != "" {
		t.Fatal(got)
	}
}
func TestRetentionRace(t *testing.T) {
	s, owner, actor, a, d := retentionFixture(t)
	read := make(chan struct{})
	resume := make(chan struct{})
	s.BeforeRetentionPublish = func() { close(read); <-resume }
	var wg sync.WaitGroup
	wg.Add(1)
	var reconciled int
	var evalErr error
	go func() { defer wg.Done(); reconciled, evalErr = s.ReconcileRetention(context.Background(), 1) }()
	<-read
	_, e := owner.Exec(`UPDATE invoyis.archives SET jurisdiction='IE' WHERE id=$1`, a)
	if e != nil {
		t.Fatal(e)
	}
	close(resume)
	wg.Wait()
	if evalErr != nil || reconciled != 0 {
		t.Fatal("old job published", reconciled, evalErr)
	}
	checkCurrent(t, s, actor, a, d, "missing")
	s.BeforeRetentionPublish = nil
	drainRetention(t, s)
	checkCurrent(t, s, actor, a, d, "current")
}
func TestRetentionTaxYearIsolationAndConcurrentCorrections(t *testing.T) {
	s, owner, actor, a, d := retentionFixture(t)
	ctx := context.Background()
	viewer := addAccount(t, owner, "retention-viewer@example.test")
	_, e := owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a, viewer)
	if e != nil {
		t.Fatal(e)
	}
	checkCurrent(t, s, viewer, a, d, "missing")
	if _, e = s.SetRetentionDocumentFacts(ctx, viewer, a, d, 0, "company", 2024, "viewer guess"); !errors.Is(e, ErrForbidden) {
		t.Fatalf("viewer wrote document facts: %v", e)
	}
	event := &TaxYearEvent{Date: retention.Date("2024-12-31"), Source: "synthetic"}
	if _, e = s.PutTaxYearFacts(ctx, viewer, a, 2024, 0, event, nil); !errors.Is(e, ErrForbidden) {
		t.Fatalf("viewer wrote tax facts: %v", e)
	}
	first, e := s.PutTaxYearFacts(ctx, actor, a, 2024, 0, event, nil)
	if e != nil || first.Version != 1 {
		t.Fatal(first, e)
	}
	replay, e := s.PutTaxYearFacts(ctx, actor, a, 2024, 1, event, nil)
	if e != nil || replay.Version != 1 {
		t.Fatal(replay, e)
	}
	other, e := s.Create(ctx, actor, "business", "Other archive", "FR")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.PutTaxYearFacts(ctx, actor, other.ID, 2024, 0, event, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetRetentionDocumentFacts(ctx, actor, other.ID, d, 0, "company", 2024, "synthetic"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatalf("cross archive fact write: %v", e)
	}
	type result struct {
		f TaxYearFacts
		e error
	}
	results := make(chan result, 2)
	for _, day := range []retention.Date{"2025-01-31", "2025-02-28"} {
		go func(day retention.Date) {
			f, e := s.PutTaxYearFacts(ctx, actor, a, 2024, 1, event, &TaxYearEvent{Date: day, Source: "synthetic"})
			results <- result{f, e}
		}(day)
	}
	x, y := <-results, <-results
	if (x.e == nil) == (y.e == nil) {
		t.Fatalf("concurrent corrections %+v %+v", x, y)
	}
	if x.e != nil && !errors.Is(x.e, ErrConflict) || y.e != nil && !errors.Is(y.e, ErrConflict) {
		t.Fatalf("unexpected error %+v %+v", x, y)
	}
	var count, version int
	e = owner.QueryRow(`SELECT count(*),max(version) FROM invoyis.archive_tax_years WHERE archive_id=$1 AND tax_year=2024`, a).Scan(&count, &version)
	if e != nil || count != 1 || version != 2 {
		t.Fatal(count, version, e)
	}
	var hist int
	e = owner.QueryRow(`SELECT count(*) FROM invoyis.archive_tax_year_history WHERE archive_id=$1 AND tax_year=2024`, a).Scan(&hist)
	if e != nil || hist != 2 {
		t.Fatal(hist, e)
	}
}

// Runs only in the separate database migrated from v18 with a confirmed record.
func TestRetentionPreexistingMigration(t *testing.T) {
	ownerURL := os.Getenv("RETENTION_PREEXISTING_DATABASE_URL")
	runtimeURL := os.Getenv("RETENTION_PREEXISTING_RUNTIME_URL")
	if ownerURL == "" || runtimeURL == "" {
		t.Skip("preexisting migration database not configured")
	}
	if !strings.Contains(ownerURL, "_test") {
		t.Fatal("refusing non-test database")
	}
	owner, e := sql.Open("pgx", ownerURL)
	if e != nil {
		t.Fatal(e)
	}
	defer owner.Close()
	runtime, e := sql.Open("pgx", runtimeURL)
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.Close()
	s := &Service{DB: runtime, Now: time.Now}
	var actor int64
	var a, d string
	e = owner.QueryRow(`SELECT a.created_by,a.id,d.id FROM invoyis.archives a JOIN invoyis.documents d ON d.archive_id=a.id WHERE a.name='Preexisting retention fixture'`).Scan(&actor, &a, &d)
	if e != nil {
		t.Fatal(e)
	}
	checkCurrent(t, s, actor, a, d, "missing")
	drainRetention(t, s)
	got := checkCurrent(t, s, actor, a, d, "current")
	if got.Snapshot.Outcome.Status != "pending" {
		t.Fatal(got)
	}
}
func TestRetentionClaimRetryIsIdempotent(t *testing.T) {
	s, owner, actor, a, d := retentionFixture(t)
	ctx := context.Background()
	// A process dies after claiming but before publishing. The lease makes work recoverable.
	rows, e := s.DB.QueryContext(ctx, `SELECT archive_id,document_id,generation,claim_token,actor_id FROM invoyis.claim_retention_work(1)`)
	if e != nil {
		t.Fatal(e)
	}
	var ca, cd string
	var claimToken string
	var generation, claimedActor int64
	if !rows.Next() {
		rows.Close()
		t.Fatal("confirmation did not enqueue")
	}
	e = rows.Scan(&ca, &cd, &generation, &claimToken, &claimedActor)
	rows.Close()
	if e != nil || ca != a || cd != d || claimedActor != actor {
		t.Fatal(ca, cd, generation, claimedActor, e)
	}
	n, e := s.ReconcileRetention(ctx, 1)
	if e != nil || n != 0 {
		t.Fatalf("unexpired claim reused %d %v", n, e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.retention_queue SET lease_until=now()-interval '1 second' WHERE archive_id=$1 AND document_id=$2`, a, d); e != nil {
		t.Fatal(e)
	}
	drainRetention(t, s)
	first := checkCurrent(t, s, actor, a, d, "current")
	second, e := s.EvaluateRetention(ctx, actor, a, d)
	if e != nil || second.ID != first.Snapshot.ID {
		t.Fatal("retry duplicated", second, e)
	}
	var count int
	e = owner.QueryRow(`SELECT count(*) FROM invoyis.retention_evaluations WHERE archive_id=$1 AND document_id=$2`, a, d).Scan(&count)
	if e != nil || count != 1 {
		t.Fatal(count, e)
	}
}

func claimRetentionForTest(t *testing.T, s *Service) (string, string, int64, string, int64) {
	t.Helper()
	var archiveID, documentID, token string
	var generation, actor int64
	if err := s.DB.QueryRowContext(context.Background(), `SELECT archive_id,document_id,generation,claim_token,actor_id FROM invoyis.claim_retention_work(1)`).Scan(&archiveID, &documentID, &generation, &token, &actor); err != nil {
		t.Fatal(err)
	}
	return archiveID, documentID, generation, token, actor
}
func ackRetentionForTest(t *testing.T, s *Service, archiveID, documentID, token string) bool {
	t.Helper()
	var acknowledged bool
	if err := s.DB.QueryRowContext(context.Background(), `SELECT invoyis.ack_retention_work($1,$2,$3)`, archiveID, documentID, token).Scan(&acknowledged); err != nil {
		t.Fatal(err)
	}
	return acknowledged
}
func TestRetentionClaimTokenRejectsExpiredAndRecreatedWork(t *testing.T) {
	s, owner, actor, a, d := retentionFixture(t)
	ca, cd, firstGeneration, oldToken, claimedActor := claimRetentionForTest(t, s)
	if ca != a || cd != d || claimedActor != actor {
		t.Fatal("wrong claim", ca, cd, claimedActor)
	}
	if _, err := owner.Exec(`UPDATE invoyis.retention_queue SET lease_until=now()-interval '1 second' WHERE archive_id=$1 AND document_id=$2`, a, d); err != nil {
		t.Fatal(err)
	}
	_, _, secondGeneration, newToken, _ := claimRetentionForTest(t, s)
	if firstGeneration != secondGeneration || oldToken == newToken {
		t.Fatal("reclaim did not rotate token")
	}
	if ackRetentionForTest(t, s, a, d, oldToken) {
		t.Fatal("expired claim deleted newer lease")
	}
	if _, err := s.EvaluateRetention(context.Background(), actor, a, d); err != nil {
		t.Fatal(err)
	}
	if !ackRetentionForTest(t, s, a, d, newToken) {
		t.Fatal("current claim was not acknowledged")
	}
	// Recreating a deleted row resets generation, but the old token stays invalid.
	if _, err := owner.Exec(`SELECT invoyis.queue_retention($1,$2)`, a, d); err != nil {
		t.Fatal(err)
	}
	var recreatedGeneration int64
	if err := owner.QueryRow(`SELECT generation FROM invoyis.retention_queue WHERE archive_id=$1 AND document_id=$2`, a, d).Scan(&recreatedGeneration); err != nil || recreatedGeneration != firstGeneration {
		t.Fatal(recreatedGeneration, err)
	}
	if ackRetentionForTest(t, s, a, d, oldToken) {
		t.Fatal("old token deleted recreated work")
	}
	_, _, _, recreatedToken, _ := claimRetentionForTest(t, s)
	if recreatedToken == oldToken || recreatedToken == newToken {
		t.Fatal("claim token reused")
	}
	if !ackRetentionForTest(t, s, a, d, recreatedToken) {
		t.Fatal("recreated work could not be acknowledged")
	}
}
func TestRetentionClaimTokenInvalidatedByInputChange(t *testing.T) {
	s, owner, _, a, d := retentionFixture(t)
	_, _, _, oldToken, _ := claimRetentionForTest(t, s)
	if _, err := owner.Exec(`UPDATE invoyis.archives SET jurisdiction='IE' WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	if ackRetentionForTest(t, s, a, d, oldToken) {
		t.Fatal("old claim deleted work after country change")
	}
	_, _, _, newToken, _ := claimRetentionForTest(t, s)
	if oldToken == newToken {
		t.Fatal("input change reused claim token")
	}
}
func TestRetentionRemovedAdminKeepsJobForAnotherAdmin(t *testing.T) {
	s, owner, actor, a, d := retentionFixture(t)
	second := addAccount(t, owner, "retention-second-admin@example.test")
	if _, err := owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'admin')`, a, second); err != nil {
		t.Fatal(err)
	}
	claimed := make(chan struct{})
	resume := make(chan struct{})
	s.BeforeRetentionJob = func() { close(claimed); <-resume }
	var wg sync.WaitGroup
	wg.Add(1)
	var done int
	var reconcileErr error
	go func() { defer wg.Done(); done, reconcileErr = s.ReconcileRetention(context.Background(), 1) }()
	<-claimed
	if _, err := owner.Exec(`DELETE FROM invoyis.archive_memberships WHERE archive_id=$1 AND account_id=$2`, a, actor); err != nil {
		t.Fatal(err)
	}
	close(resume)
	wg.Wait()
	if reconcileErr != nil || done != 0 {
		t.Fatalf("removed admin acknowledged work: %d %v", done, reconcileErr)
	}
	var queued int
	if err := owner.QueryRow(`SELECT count(*) FROM invoyis.retention_queue WHERE archive_id=$1 AND document_id=$2`, a, d).Scan(&queued); err != nil || queued != 1 {
		t.Fatal(queued, err)
	}
	s.BeforeRetentionJob = nil
	if _, err := owner.Exec(`UPDATE invoyis.retention_queue SET lease_until=now()-interval '1 second' WHERE archive_id=$1 AND document_id=$2`, a, d); err != nil {
		t.Fatal(err)
	}
	drainRetention(t, s)
	checkCurrent(t, s, second, a, d, "current")
}
func TestRetentionEventProvenanceIsServerAssigned(t *testing.T) {
	s, owner, actor, a, _ := retentionFixture(t)
	_ = owner
	firstTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s.Now = func() time.Time { return firstTime }
	forged := func(date retention.Date) *TaxYearEvent {
		return &TaxYearEvent{Date: date, Source: "synthetic", ActorID: 999999, RecordedAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)}
	}
	first, err := s.PutTaxYearFacts(context.Background(), actor, a, 2024, 0, forged("2024-12-31"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.FinancialYearEnd.ActorID != actor || !first.FinancialYearEnd.RecordedAt.Equal(firstTime) {
		t.Fatal("forged initial provenance persisted", first)
	}
	secondTime := firstTime.Add(time.Hour)
	s.Now = func() time.Time { return secondTime }
	second, err := s.PutTaxYearFacts(context.Background(), actor, a, 2024, 1, forged("2024-12-31"), forged("2025-01-31"))
	if err != nil {
		t.Fatal(err)
	}
	if !second.FinancialYearEnd.RecordedAt.Equal(firstTime) || second.FinancialYearEnd.ActorID != actor {
		t.Fatal("unchanged provenance overwritten", second)
	}
	if second.FilingDate.ActorID != actor || !second.FilingDate.RecordedAt.Equal(secondTime) {
		t.Fatal("forged filing provenance persisted", second)
	}
	thirdTime := secondTime.Add(time.Hour)
	s.Now = func() time.Time { return thirdTime }
	third, err := s.PutTaxYearFacts(context.Background(), actor, a, 2024, 2, forged("2025-12-31"), forged("2025-01-31"))
	if err != nil {
		t.Fatal(err)
	}
	if third.FinancialYearEnd.ActorID != actor || !third.FinancialYearEnd.RecordedAt.Equal(thirdTime) || !third.FilingDate.RecordedAt.Equal(secondTime) {
		t.Fatal("correction provenance wrong", third)
	}
}

func TestRetentionCoverageReleaseReconcilesAndPreservesHistory(t *testing.T) {
	s, owner, actor, a, d := retentionFixture(t)
	ctx := context.Background()
	_, err := s.SetRetentionDocumentFacts(ctx, actor, a, d, 0, "company", 2024, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	firstSet, _ := newID()
	if _, err = owner.Exec(`INSERT INTO invoyis.retention_rule_sets(version) VALUES($1)`, firstSet); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`INSERT INTO invoyis.retention_rule_versions(rule_id,version,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active) VALUES($1,1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','synthetic','issue_date',2,true)`, firstSet+"rule", firstSet); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.retention_active_rule_set SET version=$1 WHERE singleton=true`, firstSet); err != nil {
		t.Fatal(err)
	}
	drainRetention(t, s)
	old := checkCurrent(t, s, actor, a, d, "current").Snapshot
	if old.Outcome.Status != "pending" || old.Outcome.LowerBound != "2026-02-28" || old.Outcome.KeepThrough != "" {
		t.Fatal(old)
	}
	secondSet, _ := newID()
	if _, err = owner.Exec(`INSERT INTO invoyis.retention_rule_sets(version) VALUES($1)`, secondSet); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`INSERT INTO invoyis.retention_rule_versions(rule_id,version,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active) VALUES($1,1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','synthetic','issue_date',2,true)`, secondSet+"rule", secondSet); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`INSERT INTO invoyis.retention_coverage(coverage_id,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,status,source_reference) VALUES($1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','complete','synthetic')`, secondSet+"coverage", secondSet); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.retention_active_rule_set SET version=$1 WHERE singleton=true`, secondSet); err != nil {
		t.Fatal(err)
	}
	checkCurrent(t, s, actor, a, d, "stale")
	drainRetention(t, s)
	fresh := checkCurrent(t, s, actor, a, d, "current").Snapshot
	if fresh.ID == old.ID || fresh.Outcome.Status != "calculated" || fresh.Outcome.KeepThrough != "2026-02-28" || len(fresh.Outcome.Coverage) != 1 {
		t.Fatal(fresh)
	}
	var inputs, outcome []byte
	if err = owner.QueryRow(`SELECT inputs,outcome FROM invoyis.retention_evaluations WHERE id=$1`, old.ID).Scan(&inputs, &outcome); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(inputs), firstSet) || strings.Contains(string(inputs), secondSet) || !strings.Contains(string(outcome), `"status": "pending"`) {
		t.Fatalf("historical snapshot changed: %s %s", inputs, outcome)
	}
	again, err := s.EvaluateRetention(ctx, actor, a, d)
	if err != nil || again.ID != fresh.ID {
		t.Fatalf("non-idempotent: %+v %v", again, err)
	}
}

func TestRetentionCoveragePublicationRejectsContradiction(t *testing.T) {
	_, owner, _, _, _ := retentionFixture(t)
	version, _ := newID()
	if _, err := owner.Exec(`INSERT INTO invoyis.retention_rule_sets(version) VALUES($1)`, version); err != nil {
		t.Fatal(err)
	}
	for _, declaration := range []struct{ id, status string }{{"complete", "complete"}, {"excluded", "excluded"}} {
		if _, err := owner.Exec(`INSERT INTO invoyis.retention_coverage(coverage_id,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,status,source_reference) VALUES($1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date',$3,'synthetic')`, version+declaration.id, version, declaration.status); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := owner.Exec(`UPDATE invoyis.retention_active_rule_set SET version=$1 WHERE singleton=true`, version); err == nil {
		t.Fatal("contradictory coverage published")
	}
}
