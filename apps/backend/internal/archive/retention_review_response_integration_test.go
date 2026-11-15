package archive

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// The captured payload can be fed to the browser check without hand-writing
// the empty reviewBlocker field that the real Go encoder emits.
func TestEligibleRetentionHTTPResponse(t *testing.T) {
	s, owner, actor, archiveID, documentID := retentionFixture(t)
	ctx := context.Background()
	if _, err := s.SetRetentionDocumentFacts(ctx, actor, archiveID, documentID, 0, "company", 0, ""); err != nil {
		t.Fatal(err)
	}
	version, _ := newID()
	if _, err := owner.Exec(`INSERT INTO invoyis.retention_rule_sets(version) VALUES($1)`, version); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(`INSERT INTO invoyis.retention_rule_versions(rule_id,version,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active) VALUES($1,1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','synthetic','issue_date',0,true)`, version+"rule", version); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(`INSERT INTO invoyis.retention_coverage(coverage_id,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,status,source_reference) VALUES($1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','complete','synthetic')`, version+"coverage", version); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(`UPDATE invoyis.retention_active_rule_set SET version=$1 WHERE singleton=true`, version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EvaluateRetention(ctx, actor, archiveID, documentID); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC) }
	token := "eligible-retention-session"
	digest := sha256.Sum256([]byte(token))
	if _, err := owner.Exec(`INSERT INTO invoyis.sessions(account_id,token_digest,expires_at) VALUES($1,$2,$3)`, actor, digest[:], time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	req := httptest.NewRequest("GET", "/v1/archives/"+archiveID+"/documents/"+documentID+"/retention", nil)
	req.Header.Set("X-Invoyis-Gateway-Key", s.Auth.GatewaySecret)
	req.Header.Set("X-Invoyis-Client-IP", "127.0.0.1")
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reviewBlocker":""`) {
		t.Fatalf("eligible API payload: %d %s", w.Code, w.Body.String())
	}
	report, err := s.StorageReport(ctx, actor, archiveID, "category", "review")
	if err != nil || report.DocumentCount != 1 || report.OriginalCount != 1 {
		t.Fatalf("eligible storage report: %+v %v", report, err)
	}
	if path := os.Getenv("STORAGE_BROWSER_FIXTURE_OUTPUT"); path != "" {
		data, err := json.Marshal(map[string]string{"archiveId": archiveID, "documentId": documentID, "token": token})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if path := os.Getenv("RETENTION_RESPONSE_OUTPUT"); path != "" {
		if err := os.WriteFile(path, w.Body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
