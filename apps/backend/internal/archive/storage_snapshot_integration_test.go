package archive

import (
	"context"
	"testing"
	"time"
)

// The correction and its new evaluation commit after the filing rows are read.
// A report must see either the old revision and old decision, or the new pair.
func TestStorageReviewUsesOneSnapshotAcrossCorrection(t *testing.T) {
	s, owner, actor, archiveID, documentID := retentionFixture(t)
	ctx := context.Background()
	oldCategory, _, err := s.CreateOrganisation(ctx, actor, archiveID, "categories", "Old category")
	if err != nil {
		t.Fatal(err)
	}
	newCategory, _, err := s.CreateOrganisation(ctx, actor, archiveID, "categories", "New category")
	if err != nil {
		t.Fatal(err)
	}
	metadata := ReviewMetadata{DocumentType: TypeLease, Title: "Synthetic lease", IssueDate: "2024-02-29"}
	if _, err = reviewFilingWrite(t, s, actor, archiveID, documentID, "confirm", 1, metadata, ReviewFiling{CategoryID: filingID(oldCategory.ID)}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRetentionDocumentFacts(ctx, actor, archiveID, documentID, 0, "company", 0, ""); err != nil {
		t.Fatal(err)
	}
	version, _ := newID()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO invoyis.retention_rule_sets(version) VALUES($1)`, []any{version}},
		{`INSERT INTO invoyis.retention_rule_versions(rule_id,version,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active) VALUES($1,1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','synthetic','issue_date',0,true)`, []any{version + "rule", version}},
		{`INSERT INTO invoyis.retention_coverage(coverage_id,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,status,source_reference) VALUES($1,$2,'FR','company','lease','2020-01-01','2030-01-01','issue_date','complete','synthetic')`, []any{version + "coverage", version}},
		{`UPDATE invoyis.retention_active_rule_set SET version=$1 WHERE singleton=true`, []any{version}},
	} {
		if _, err = owner.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	s.Now = func() time.Time { return time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC) }
	refs, err := s.storageReferences(ctx, actor, archiveID, "category", "review", func() {
		if _, e := reviewFilingWrite(t, s, actor, archiveID, documentID, "confirm", 2, metadata, ReviewFiling{CategoryID: filingID(newCategory.ID)}); e != nil {
			t.Fatal(e)
		}
		if _, e := s.EvaluateRetention(ctx, actor, archiveID, documentID); e != nil {
			t.Fatal(e)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	old := aggregateStorage(refs, "category", "review")
	if old.DocumentCount != 0 || old.Bytes != 0 {
		t.Fatalf("mixed old filing and new decision: %+v", old)
	}
	current, err := s.StorageReport(ctx, actor, archiveID, "category", "review")
	if err != nil {
		t.Fatal(err)
	}
	if current.DocumentCount != 1 || current.Bytes == 0 || len(current.Groups) != 1 || current.Groups[0].ID != newCategory.ID {
		t.Fatalf("new filing and decision: %+v", current)
	}
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	plain, err := s.readRetentionCandidate(ctx, tx, archiveID, documentID)
	if err != nil {
		t.Fatal(err)
	}
	catalogue, err := loadRetentionCatalogue(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := s.readRetentionCandidateWithCatalogue(ctx, tx, archiveID, documentID, catalogue)
	if err != nil || cached.fingerprint != plain.fingerprint || string(cached.encoded) != string(plain.encoded) {
		t.Fatalf("cached candidate differs: %+v %+v %v", plain, cached, err)
	}
}
