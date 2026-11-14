package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"testing"
)

func TestExportRetentionUsesConfirmedRevisionSnapshot(t *testing.T) {
	s, owner, actor, archiveID, documentID := retentionFixture(t)
	ctx := context.Background()
	if _, err := owner.Exec(`UPDATE invoyis.archive_memberships SET can_bulk_export=true WHERE archive_id=$1 AND account_id=$2`, archiveID, actor); err != nil {
		t.Fatal(err)
	}
	original, err := s.EvaluateRetention(ctx, actor, archiveID, documentID)
	if err != nil {
		t.Fatal(err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	s.BeforeExportRetention = func() { close(entered); <-resume }
	type result struct {
		items []exportItem
		err   error
	}
	done := make(chan result, 1)
	go func() { items, err := s.exportSnapshot(ctx, actor, archiveID); done <- result{items, err} }()
	<-entered
	_, err = write(t, s, actor, archiveID, documentID, "confirm", 1, ReviewMetadata{DocumentType: TypeLease, Title: "Corrected lease", IssueDate: "2025-01-01"})
	if err != nil {
		close(resume)
		t.Fatal(err)
	}
	corrected, err := s.EvaluateRetention(ctx, actor, archiveID, documentID)
	if err != nil {
		close(resume)
		t.Fatal(err)
	}
	close(resume)
	exported := <-done
	if exported.err != nil || len(exported.items) != 1 {
		t.Fatalf("export: %+v", exported)
	}
	item := exported.items[0]
	if item.Metadata.Title != "Synthetic lease" || item.RetentionDecisionID != original.ID || item.RetentionRevisionID != original.ConfirmedRevisionID || item.RevisionID != original.ConfirmedRevisionID || item.RetentionDecisionID == corrected.ID {
		t.Fatalf("mixed revision export: %+v, corrected=%+v", item, corrected)
	}
}

func TestPersonalExportIncludesReminderAndRevisionBoundDecision(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	if _, err := owner.Exec(`UPDATE invoyis.retention_active_rule_set SET version='empty-v1' WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	a, err := s.Create(ctx, actor, "personal", "Personal export retention", "FR")
	if err != nil {
		t.Fatal(err)
	}
	documentID, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "personal-export")
	if _, err = write(t, s, actor, a.ID, documentID, "confirm", 0, ReviewMetadata{DocumentType: TypeLease, Title: "Synthetic Personal lease", IssueDate: "2020-01-01"}); err != nil {
		t.Fatal(err)
	}
	version, _ := newID()
	if _, err = owner.Exec(`INSERT INTO invoyis.retention_rule_sets(version) VALUES($1)`, version); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`INSERT INTO invoyis.retention_rule_versions(rule_id,version,rule_set_version,archive_kind,jurisdiction,entity_scope,document_scope,effective_from,selection_date,source_reference,trigger_event,calendar_years,active) VALUES($1,1,$2,'personal','FR','person','lease','2010-01-01','issue_date','synthetic Personal export','issue_date',5,true)`, version+"rule", version); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`INSERT INTO invoyis.retention_coverage(coverage_id,rule_set_version,archive_kind,jurisdiction,entity_scope,document_scope,effective_from,selection_date,status,source_reference) VALUES($1,$2,'personal','FR','person','lease','2010-01-01','issue_date','complete','synthetic Personal export')`, version+"coverage", version); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.retention_active_rule_set SET version=$1 WHERE singleton=true`, version); err != nil {
		t.Fatal(err)
	}
	decision, err := s.EvaluateRetention(ctx, actor, a.ID, documentID)
	if err != nil || decision.Outcome.Status != "calculated" {
		t.Fatalf("decision: %+v %v", decision, err)
	}
	reminder := "2030-01-01"
	if err = s.SetPersonalReminder(ctx, actor, a.ID, documentID, &reminder); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.archive_memberships SET can_bulk_export=true WHERE archive_id=$1 AND account_id=$2`, a.ID, actor); err != nil {
		t.Fatal(err)
	}
	items, err := s.exportSnapshot(ctx, actor, a.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("export: %+v %v", items, err)
	}
	item := items[0]
	if item.Reminder != reminder || item.RetentionState != "calculated" || item.RetentionDecisionID != decision.ID || item.RetentionRevisionID != item.RevisionID || item.RetentionRuleSet != version || item.RetentionKeepThrough != "2025-01-01" {
		t.Fatalf("Personal export omitted decision: %+v", item)
	}
	s.Bundles = &fakeBundles{objects: map[string][]byte{}}
	receipt, err := s.CreateExport(ctx, actor, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := s.DownloadExport(ctx, actor, a.ID, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		if file.Name != "index.csv" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(bytes.NewReader(content)).ReadAll()
		if err != nil || len(rows) != 2 {
			t.Fatalf("index rows: %v %v", rows, err)
		}
		values := map[string]string{}
		for i, column := range rows[0] {
			values[column] = rows[1][i]
		}
		if values["retention_decision_id"] != decision.ID || values["retention_revision_id"] != item.RevisionID || values["retention_rule_set"] != version || values["retention_keep_through"] != "2025-01-01" || values["reminder"] != reminder {
			t.Fatalf("ZIP omitted Personal retention: %+v", values)
		}
		return
	}
	t.Fatal("ZIP omitted index.csv")
}
