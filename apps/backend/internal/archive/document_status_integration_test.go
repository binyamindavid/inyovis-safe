package archive

import (
	"context"
	"errors"
	"testing"
)

func TestDocumentStatusArchiveBoundary(t *testing.T) {
	service, _, owner, admin := setup(t)
	ctx := context.Background()
	storage := &fakeOriginals{objects: map[string][]byte{}}
	service.Storage = storage
	archive, err := service.Create(ctx, admin, "personal", "Status test", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.Create(ctx, admin, "personal", "Other archive", "")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("%PDF-1.7\nsynthetic status\n%%EOF")
	session, err := service.CreateUpload(ctx, admin, archive.ID, claim(data, "status"))
	if err != nil {
		t.Fatal(err)
	}
	storage.objects[session.Grant.Fields["key"]] = data
	receipt, err := service.CompleteUpload(ctx, admin, archive.ID, session.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	found, err := service.DocumentStatus(ctx, admin, archive.ID, receipt.DocumentID)
	if err != nil || found.Status != "pending" {
		t.Fatalf("pending status %+v %v", found, err)
	}
	viewer := addAccount(t, owner, "status-viewer@example.test")
	if _, err = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, archive.ID, viewer); err != nil {
		t.Fatal(err)
	}
	visible, err := service.DocumentStatus(ctx, viewer, archive.ID, receipt.DocumentID)
	if err != nil || visible.Status != "pending" {
		t.Fatalf("viewer status %+v %v", visible, err)
	}
	viewerList, err := service.ListDocumentStatuses(ctx, viewer, archive.ID)
	if err != nil || len(viewerList.Items) != 1 {
		t.Fatalf("viewer list %+v %v", viewerList, err)
	}
	for _, id := range []string{other.ID, "00000000-0000-0000-0000-000000000001"} {
		if _, err = service.DocumentStatus(ctx, admin, id, receipt.DocumentID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("archive %s: %v", id, err)
		}
	}
	if _, err = service.DocumentStatus(ctx, admin, archive.ID, "00000000-0000-0000-0000-000000000001"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing document: %v", err)
	}
	list, err := service.ListDocumentStatuses(ctx, admin, archive.ID)
	if err != nil || len(list.Items) != 1 || list.Items[0].ID != receipt.DocumentID {
		t.Fatalf("list %+v %v", list, err)
	}
	if _, err = owner.Exec(`UPDATE invoyis.processing_jobs SET state='running',attempts=1,lease_token=gen_random_uuid(),lease_expires_at=now()+interval '1 minute' WHERE document_id=$1`, receipt.DocumentID); err != nil {
		t.Fatal(err)
	}
	processing, err := service.DocumentStatus(ctx, admin, archive.ID, receipt.DocumentID)
	if err != nil || processing.Status != "processing" {
		t.Fatalf("processing %+v %v", processing, err)
	}
}

func TestSafeDocumentStatusMapping(t *testing.T) {
	cases := []struct{ job, document, clearance, failure, want string }{
		{"pending", "received", "pending", "", "pending"},
		{"running", "extracting", "pending", "", "processing"},
		{"succeeded", "needs_review", "cleared", "", "needs_review"},
		{"failed", "failed", "blocked", "scan_rejected", "file_blocked"},
		{"failed", "failed", "blocked", "invalid_pdf", "processing_failed"},
	}
	for _, c := range cases {
		if got := documentStatus(c.job, c.document, c.clearance, c.failure); got != c.want {
			t.Fatalf("%+v got %s", c, got)
		}
	}
}
