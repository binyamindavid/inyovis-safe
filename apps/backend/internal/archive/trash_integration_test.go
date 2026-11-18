package archive

import (
	"context"
	"errors"
	"testing"
)

func TestTrashRestoreRestrictedRoleAndArchiveIsolation(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	a, e := s.Create(ctx, actor, "personal", "Trash fixture", "")
	if e != nil {
		t.Fatal(e)
	}
	doc, _, _ := clearedUpload(t, s, owner, store, actor, a.ID, "unwanted")
	before, e := s.DocumentStatus(ctx, actor, a.ID, doc)
	if e != nil {
		t.Fatal(e)
	}
	viewer := addAccount(t, owner, "trash-viewer@example.test")
	if _, e = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a.ID, viewer); e != nil {
		t.Fatal(e)
	}
	if _, e = s.MoveToTrash(ctx, viewer, a.ID, doc, before.OriginalFilename, "unwanted upload"); !errors.Is(e, ErrForbidden) {
		t.Fatalf("viewer trash: %v", e)
	}
	tx, e := s.transaction(ctx, viewer, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	var deniedDeadline string
	e = tx.QueryRowContext(ctx, `SELECT invoyis.move_document_to_trash($1,$2,$3)`, a.ID, doc, "forged").Scan(&deniedDeadline)
	_ = tx.Rollback()
	if e == nil {
		t.Fatal("database allowed viewer trash")
	}
	other, e := s.Create(ctx, actor, "personal", "Other", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.MoveToTrash(ctx, actor, other.ID, doc, before.OriginalFilename, "wrong archive"); !errors.Is(e, ErrNotFound) {
		t.Fatalf("cross archive trash: %v", e)
	}
	if _, e = s.MoveToTrash(ctx, actor, a.ID, doc, "wrong name", "unwanted upload"); !errors.Is(e, ErrConflict) {
		t.Fatalf("name mismatch: %v", e)
	}
	deadline, e := s.MoveToTrash(ctx, actor, a.ID, doc, before.OriginalFilename, "unwanted upload")
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.MoveToTrash(ctx, actor, a.ID, doc, before.OriginalFilename, "unwanted upload")
	if e != nil || !again.Equal(deadline) {
		t.Fatalf("idempotent trash: %v %v", again, e)
	}
	if _, e = s.DocumentStatus(ctx, actor, a.ID, doc); !errors.Is(e, ErrNotFound) {
		t.Fatalf("status visible: %v", e)
	}
	listed, e := s.ListTrash(ctx, actor, a.ID)
	if e != nil || len(listed) != 1 || listed[0].ID != doc {
		t.Fatalf("trash list: %+v %v", listed, e)
	}
	if e = s.RestoreFromTrash(ctx, viewer, a.ID, doc); !errors.Is(e, ErrForbidden) {
		t.Fatalf("viewer restore: %v", e)
	}
	if e = s.RestoreFromTrash(ctx, actor, other.ID, doc); e == nil {
		t.Fatal("cross archive restore")
	}
	if e = s.RestoreFromTrash(ctx, actor, a.ID, doc); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DocumentStatus(ctx, actor, a.ID, doc); e != nil {
		t.Fatalf("restored status: %v", e)
	}
	var events int
	if e = owner.QueryRow(`SELECT count(*) FROM invoyis.document_trash_events WHERE archive_id=$1 AND document_id=$2`, a.ID, doc).Scan(&events); e != nil || events != 2 {
		t.Fatalf("events=%d %v", events, e)
	}
}
