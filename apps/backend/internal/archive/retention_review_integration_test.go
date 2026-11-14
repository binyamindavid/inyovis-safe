package archive

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDocumentHoldsHistoryAndRuntimeIsolation(t *testing.T) {
	s, owner, admin, a, d := retentionFixture(t)
	ctx := context.Background()
	viewer := addAccount(t, owner, "holds-viewer@example.test")
	if _, e := owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a, viewer); e != nil {
		t.Fatal(e)
	}
	if _, e := s.PlaceDocumentHold(ctx, viewer, a, d, "viewer denied"); !errors.Is(e, ErrForbidden) {
		t.Fatalf("viewer placed hold: %v", e)
	}
	first, e := s.PlaceDocumentHold(ctx, admin, a, d, "litigation")
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.PlaceDocumentHold(ctx, admin, a, d, "audit")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReleaseDocumentHold(ctx, admin, a, d, first.ID, " "); !errors.Is(e, ErrInvalid) {
		t.Fatalf("release without reason: %v", e)
	}
	tx, e := s.transaction(ctx, admin, a)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.ExecContext(ctx, `UPDATE invoyis.document_holds SET released_by=$2,released_at=now() WHERE id=$1`, first.ID, admin)
	_ = tx.Rollback()
	if e == nil {
		t.Fatal("database accepted a release without a reason")
	}
	if e = s.ReleaseDocumentHold(ctx, admin, a, d, first.ID, "resolved litigation"); e != nil {
		t.Fatal(e)
	}
	details, e := s.RetentionDetails(ctx, admin, a, d)
	if e != nil {
		t.Fatal(e)
	}
	if len(details.Holds) != 2 || details.Holds[0].ReleasedAt != nil && details.Holds[1].ReleasedAt != nil {
		t.Fatalf("holds: %+v", details.Holds)
	}
	active := 0
	for _, h := range details.Holds {
		if h.ReleasedAt == nil {
			active++
		} else if h.ReleaseReason == nil || *h.ReleaseReason != "resolved litigation" {
			t.Fatalf("release reason missing from history: %+v", h)
		}
	}
	if active != 1 {
		t.Fatalf("active=%d blocker=%s", active, details.ReviewBlocker)
	}
	if e = s.ReleaseDocumentHold(ctx, admin, a, d, second.ID, "audit completed"); e != nil {
		t.Fatal(e)
	}
	other, e := s.Create(ctx, admin, "business", "Other", "IE")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.RetentionDetails(ctx, viewer, other.ID, d); !errors.Is(e, ErrForbidden) && !errors.Is(e, ErrNotFound) {
		t.Fatalf("cross archive read: %v", e)
	}
	tx, e = s.transaction(ctx, viewer, a)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	id, _ := newID()
	_, e = tx.ExecContext(ctx, `INSERT INTO invoyis.document_holds(id,archive_id,document_id,reason,placed_by) VALUES($1,$2,$3,'forged',$4)`, id, a, d, viewer)
	if e == nil {
		t.Fatal("viewer bypassed hold RLS")
	}
}
func TestPersonalReminderPermissionAndDate(t *testing.T) {
	s, _, owner, admin := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	archive, e := s.Create(ctx, admin, "personal", "Reminders", "")
	if e != nil {
		t.Fatal(e)
	}
	doc, _, _ := clearedUpload(t, s, owner, store, admin, archive.ID, "reminder")
	viewer := addAccount(t, owner, "reminder-viewer@example.test")
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, archive.ID, viewer); e != nil {
		t.Fatal(e)
	}
	date := "2026-11-14"
	if e = s.SetPersonalReminder(ctx, viewer, archive.ID, doc, &date); !errors.Is(e, ErrForbidden) {
		t.Fatalf("viewer set reminder: %v", e)
	}
	if e = s.SetPersonalReminder(ctx, admin, archive.ID, doc, &date); e != nil {
		t.Fatal(e)
	}
	details, e := s.RetentionDetails(ctx, admin, archive.ID, doc)
	if e != nil {
		t.Fatal(e)
	}
	if details.Reminder == nil || *details.Reminder != date || details.ReviewBlocker != "cleared_confirmed_document_required" {
		t.Fatalf("personal details: %+v", details)
	}
	if e = s.SetPersonalReminder(ctx, admin, archive.ID, doc, nil); e != nil {
		t.Fatal(e)
	}
	details, e = s.RetentionDetails(ctx, admin, archive.ID, doc)
	if e != nil || details.Reminder != nil {
		t.Fatalf("clear: %+v %v", details, e)
	}
}
func TestShreddingReviewRechecksAfterPageLoadAndKeepsOriginal(t *testing.T) {
	s, owner, admin, a, d := retentionFixture(t)
	ctx := context.Background()
	_, e := s.SetRetentionDocumentFacts(ctx, admin, a, d, 0, "company", 0, "")
	if e != nil {
		t.Fatal(e)
	}
	version, _ := newID()
	if _, e = owner.Exec(`INSERT INTO invoyis.retention_rule_sets(version) VALUES($1)`, version); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.retention_rule_versions(rule_id,version,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active) VALUES($1,1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','synthetic review','issue_date',0,true)`, version+"rule", version); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.retention_coverage(coverage_id,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,status,source_reference) VALUES($1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','complete','synthetic review')`, version+"coverage", version); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.retention_active_rule_set SET version=$1 WHERE singleton=true`, version); e != nil {
		t.Fatal(e)
	}
	if _, e = s.EvaluateRetention(ctx, admin, a, d); e != nil {
		t.Fatal(e)
	}
	s.Now = func() time.Time { v, _ := time.Parse("2006-01-02", "2024-02-29"); return v }
	details, e := s.RetentionDetails(ctx, admin, a, d)
	if e != nil || details.ReviewBlocker != "keep_through_date" {
		t.Fatalf("inclusive date: %+v %v", details, e)
	}
	s.Now = func() time.Time { v, _ := time.Parse("2006-01-02", "2024-03-01"); return v }
	details, e = s.RetentionDetails(ctx, admin, a, d)
	if e != nil || details.ReviewBlocker != "" {
		t.Fatalf("following date: %+v %v", details, e)
	}
	h, e := s.PlaceDocumentHold(ctx, admin, a, d, "later hold")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.OpenShreddingReview(ctx, admin, a, d); !errors.Is(e, ErrConflict) {
		t.Fatalf("later hold: %v", e)
	}
	if e = s.ReleaseDocumentHold(ctx, admin, a, d, h.ID, "hold cleared"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.OpenShreddingReview(ctx, admin, a, d); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.DownloadOriginal(ctx, admin, a, d); e != nil {
		t.Fatalf("original after review: %v", e)
	}
	if _, e = s.SetRetentionDocumentFacts(ctx, admin, a, d, 1, "company", 2024, "corrected"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.OpenShreddingReview(ctx, admin, a, d); !errors.Is(e, ErrConflict) {
		t.Fatalf("stale input: %v", e)
	}
}
func TestRetentionHTTPViewerCannotReassessOrHold(t *testing.T) {
	s, owner, _, a, d := retentionFixture(t)
	viewer := addAccount(t, owner, "retention-http-viewer@example.test")
	if _, e := owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a, viewer); e != nil {
		t.Fatal(e)
	}
	token := "retention-viewer-session"
	digest := sha256.Sum256([]byte(token))
	if _, e := owner.Exec(`INSERT INTO invoyis.sessions(account_id,token_digest,expires_at) VALUES($1,$2,$3)`, viewer, digest[:], time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	for _, tc := range []struct{ path, body string }{{"retention/reassess", ""}, {"holds", `{"reason":"forged"}`}} {
		req := httptest.NewRequest("POST", "/v1/archives/"+a+"/documents/"+d+"/"+tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Invoyis-Gateway-Key", s.Auth.GatewaySecret)
		req.Header.Set("X-Invoyis-Client-IP", "127.0.0.1")
		req.Header.Set("X-Invoyis-Intent", "browser")
		req.Header.Set("Origin", s.BrowserOrigin)
		req.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}
func TestRetentionPublicationTargetsChangedScope(t *testing.T) {
	s, owner, admin, fr, frDoc := retentionFixture(t)
	ctx := context.Background()
	store := s.Storage.(*fakeOriginals)
	ieArchive, e := s.Create(ctx, admin, "business", "Irish pilot", "IE")
	if e != nil {
		t.Fatal(e)
	}
	ieDoc, _, _ := clearedUpload(t, s, owner, store, admin, ieArchive.ID, "irish")
	if _, e = write(t, s, admin, ieArchive.ID, ieDoc, "confirm", 0, ReviewMetadata{DocumentType: TypeLease, Title: "Irish lease", IssueDate: "2024-02-29"}); e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct{ archive, doc string }{{fr, frDoc}, {ieArchive.ID, ieDoc}} {
		if _, e = s.SetRetentionDocumentFacts(ctx, admin, v.archive, v.doc, 0, "company", 0, ""); e != nil {
			t.Fatal(e)
		}
	}
	v1, _ := newID()
	v2, _ := newID()
	for _, v := range []string{v1, v2} {
		if _, e = owner.Exec(`INSERT INTO invoyis.retention_rule_sets(version) VALUES($1)`, v); e != nil {
			t.Fatal(e)
		}
	}
	for _, country := range []string{"FR", "IE"} {
		if _, e = owner.Exec(`INSERT INTO invoyis.retention_rule_versions(rule_id,version,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active) VALUES($1,1,$2,$3,'company','lease','2020-01-01','2030-01-01','issue_date','synthetic','issue_date',2,true)`, country+"-lease", v1, country); e != nil {
			t.Fatal(e)
		}
		if _, e = owner.Exec(`INSERT INTO invoyis.retention_coverage(coverage_id,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,status,source_reference) VALUES($1,$2,$3,'company','lease','2020-01-01','2030-01-01','issue_date','complete','synthetic')`, country+"-coverage", v1, country); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = owner.Exec(`UPDATE invoyis.retention_active_rule_set SET version=$1 WHERE singleton=true`, v1); e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct{ archive, doc string }{{fr, frDoc}, {ieArchive.ID, ieDoc}} {
		if _, e = s.EvaluateRetention(ctx, admin, v.archive, v.doc); e != nil {
			t.Fatal(e)
		}
	}
	frFirst := checkCurrent(t, s, admin, fr, frDoc, "current").Snapshot.ID
	if _, e = owner.Exec(`DELETE FROM invoyis.retention_queue`); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.retention_rule_versions(rule_id,version,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active,exceptions,validation_record,archive_kind) SELECT rule_id,version,$1,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active,exceptions,validation_record,archive_kind FROM invoyis.retention_rule_versions WHERE rule_set_version=$2 AND jurisdiction='FR'`, v2, v1); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.retention_coverage(coverage_id,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,status,source_reference,archive_kind) SELECT coverage_id,$1,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,status,source_reference,archive_kind FROM invoyis.retention_coverage WHERE rule_set_version=$2`, v2, v1); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`INSERT INTO invoyis.retention_rule_versions(rule_id,version,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active) VALUES('IE-lease',2,$1,'IE','company','lease','2020-01-01','2030-01-01','issue_date','synthetic revised','issue_date',3,true)`, v2); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.retention_active_rule_set SET version=$1 WHERE singleton=true`, v2); e != nil {
		t.Fatal(e)
	}
	if got := checkCurrent(t, s, admin, fr, frDoc, "current"); got.Snapshot.ID != frFirst {
		t.Fatalf("unrelated FR changed: %+v", got)
	}
	checkCurrent(t, s, admin, ieArchive.ID, ieDoc, "stale")
	var queued []string
	rows, e := owner.Query(`SELECT document_id FROM invoyis.retention_queue ORDER BY document_id`)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		queued = append(queued, id)
	}
	if len(queued) != 1 || queued[0] != ieDoc {
		t.Fatalf("targeted queue: %v", queued)
	}
}
func TestRetentionActivationRequiresVersionedRule(t *testing.T) {
	_, owner, _, _, _ := retentionFixture(t)
	version, _ := newID()
	if _, e := owner.Exec(`INSERT INTO invoyis.retention_rule_sets(version) VALUES($1)`, version); e != nil {
		t.Fatal(e)
	}
	if _, e := owner.Exec(`INSERT INTO invoyis.retention_coverage(coverage_id,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,status,source_reference) VALUES($1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','complete','synthetic')`, version+"coverage", version); e != nil {
		t.Fatal(e)
	}
	if _, e := owner.Exec(`UPDATE invoyis.retention_active_rule_set SET version=$1 WHERE singleton=true`, version); e == nil {
		t.Fatal("unvalidated complete scope activated")
	}
}
func TestHoldReleaseHTTPRequiresReason(t *testing.T) {
	s, owner, admin, archiveID, documentID := retentionFixture(t)
	hold, err := s.PlaceDocumentHold(context.Background(), admin, archiveID, documentID, "litigation")
	if err != nil {
		t.Fatal(err)
	}
	token := "hold-release-admin-session"
	digest := sha256.Sum256([]byte(token))
	if _, err = owner.Exec(`INSERT INTO invoyis.sessions(account_id,token_digest,expires_at) VALUES($1,$2,$3)`, admin, digest[:], time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	call := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/archives/"+archiveID+"/documents/"+documentID+"/holds/"+hold.ID+"/release", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Invoyis-Gateway-Key", s.Auth.GatewaySecret)
		req.Header.Set("X-Invoyis-Client-IP", "127.0.0.1")
		req.Header.Set("X-Invoyis-Intent", "browser")
		req.Header.Set("Origin", s.BrowserOrigin)
		req.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	if w := call(`{}`); w.Code != 422 {
		t.Fatalf("missing reason: %d %s", w.Code, w.Body.String())
	}
	if w := call(`{"reason":"case closed"}`); w.Code != 204 {
		t.Fatalf("release: %d %s", w.Code, w.Body.String())
	}
	details, err := s.RetentionDetails(context.Background(), admin, archiveID, documentID)
	if err != nil || len(details.Holds) != 1 || details.Holds[0].ReleaseReason == nil || *details.Holds[0].ReleaseReason != "case closed" {
		t.Fatalf("release history: %+v %v", details, err)
	}
}
