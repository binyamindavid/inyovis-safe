package archive

import (
	"context"
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
