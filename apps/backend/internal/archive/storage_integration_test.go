package archive

import (
	"context"
	"errors"
	"testing"
)

func TestStorageWholeArchiveAndIsolation(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	a, err := s.Create(ctx, actor, "personal", "Storage fixtures", "")
	if err != nil {
		t.Fatal(err)
	}
	cat1, _, err := s.CreateOrganisation(ctx, actor, a.ID, "categories", "Utilities")
	if err != nil {
		t.Fatal(err)
	}
	cat2, _, err := s.CreateOrganisation(ctx, actor, a.ID, "categories", "Office")
	if err != nil {
		t.Fatal(err)
	}
	sup, _, err := s.CreateOrganisation(ctx, actor, a.ID, "suppliers", "A supplier with a very long company name")
	if err != nil {
		t.Fatal(err)
	}
	// Twenty-three originals place records on more than one library page. A second
	// submission and document point to original 1 with a different category/type.
	statements := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO invoyis.upload_sessions(id,archive_id,uploader_id,idempotency_key,filename,media_type,declared_bytes,declared_sha256,staging_key,prepared_key,final_key,grant_expires_at,finalise_by,state,verified_bytes,verified_sha256)
 SELECT md5('storage-session'||n)::uuid,$1,$2,'storage-'||n,'storage-'||n||'.pdf','application/pdf',n,repeat(md5(n::text),2),'staging/storage/'||n,'prepared/storage/'||n,'originals/storage/'||n,now(),now(),'complete',n,repeat(md5(n::text),2) FROM generate_series(1,23) n`, []any{a.ID, actor}},
		{`INSERT INTO invoyis.stored_objects(id,archive_id,upload_session_id,storage_key,byte_count,sha256)
 SELECT md5('storage-object'||n)::uuid,$1,md5('storage-session'||n)::uuid,'originals/storage/'||n,n,repeat(md5(n::text),2) FROM generate_series(1,23) n`, []any{a.ID}},
		{`INSERT INTO invoyis.submissions(id,archive_id,upload_session_id,stored_object_id,uploader_id,source,original_filename)
 SELECT md5('storage-submission'||n)::uuid,$1,md5('storage-session'||n)::uuid,md5('storage-object'||n)::uuid,$2,'browser_upload','storage-'||n||'.pdf' FROM generate_series(1,23) n`, []any{a.ID, actor}},
		{`INSERT INTO invoyis.documents(id,archive_id,submission_id,state,clearance)
 SELECT md5('storage-document'||n)::uuid,$1,md5('storage-submission'||n)::uuid,'ready','cleared' FROM generate_series(1,23) n`, []any{a.ID}},
		{`INSERT INTO invoyis.processing_jobs(id,archive_id,document_id,state)
 SELECT md5('storage-job'||n)::uuid,$1,md5('storage-document'||n)::uuid,'succeeded' FROM generate_series(1,23) n`, []any{a.ID}},
		{`INSERT INTO invoyis.document_revisions(id,archive_id,document_id,actor_id,kind,metadata,provenance,category_id,supplier_id)
 SELECT md5('storage-revision'||n)::uuid,$1,md5('storage-document'||n)::uuid,$2,'confirmed',jsonb_build_object('documentType',CASE WHEN n=23 THEN 'bill' ELSE 'invoice' END,'title','Document '||n),'{}'::jsonb,CASE WHEN n=23 THEN NULL ELSE $3::uuid END,CASE WHEN n=23 THEN NULL ELSE $4::uuid END FROM generate_series(1,23) n`, []any{a.ID, actor, cat1.ID, sup.ID}},
		{`UPDATE invoyis.documents SET current_confirmed_revision_id=md5('storage-revision'||n)::uuid FROM generate_series(1,23) n WHERE id=md5('storage-document'||n)::uuid AND archive_id=$1`, []any{a.ID}},
		{`INSERT INTO invoyis.upload_sessions(id,archive_id,uploader_id,idempotency_key,filename,media_type,declared_bytes,declared_sha256,staging_key,prepared_key,final_key,grant_expires_at,finalise_by,state,verified_bytes,verified_sha256)
 VALUES(md5('storage-extra-session')::uuid,$1,$2,'storage-extra','storage-extra.pdf','application/pdf',1,repeat('a',64),'staging/storage/extra','prepared/storage/extra','originals/storage/extra',now(),now(),'complete',1,repeat('a',64))`, []any{a.ID, actor}},
		{`INSERT INTO invoyis.submissions(id,archive_id,upload_session_id,stored_object_id,uploader_id,source,original_filename)
 VALUES(md5('storage-extra-submission')::uuid,$1,md5('storage-extra-session')::uuid,md5('storage-object1')::uuid,$2,'browser_upload','storage-extra.pdf')`, []any{a.ID, actor}},
		{`INSERT INTO invoyis.documents(id,archive_id,submission_id,state,clearance) VALUES(md5('storage-extra-document')::uuid,$1,md5('storage-extra-submission')::uuid,'ready','cleared')`, []any{a.ID}},
		{`INSERT INTO invoyis.processing_jobs(id,archive_id,document_id,state) VALUES(md5('storage-extra-job')::uuid,$1,md5('storage-extra-document')::uuid,'succeeded')`, []any{a.ID}},
		{`INSERT INTO invoyis.document_revisions(id,archive_id,document_id,actor_id,kind,metadata,provenance,category_id)
 VALUES(md5('storage-extra-revision')::uuid,$1,md5('storage-extra-document')::uuid,$2,'confirmed','{"documentType":"bill","title":"Shared bill"}'::jsonb,'{}'::jsonb,$3)`, []any{a.ID, actor, cat2.ID}},
		{`UPDATE invoyis.documents SET current_confirmed_revision_id=md5('storage-extra-revision')::uuid WHERE id=md5('storage-extra-document')::uuid AND archive_id=$1`, []any{a.ID}},
	}
	for _, v := range statements {
		if _, err = owner.ExecContext(ctx, v.q, v.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, by := range []string{"category", "company", "format", "type"} {
		r, e := s.StorageReport(ctx, actor, a.ID, by, "overview")
		if e != nil {
			t.Fatal(e)
		}
		if r.Bytes != 276 || r.OriginalCount != 23 || r.DocumentCount != 24 {
			t.Fatalf("%s headline: %+v", by, r)
		}
		var sum int64
		var refs int
		for _, g := range r.Groups {
			sum += g.Bytes
			cursor := ""
			for {
				page, e := s.StorageGroupDocuments(ctx, actor, a.ID, by, "overview", g.ID, cursor)
				if e != nil {
					t.Fatal(e)
				}
				if page.Bytes != g.Bytes || page.DocumentCount != g.DocumentCount {
					t.Fatalf("drill-down changed group: %s %+v %+v", by, g, page)
				}
				refs += len(page.Items)
				if page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
		}
		if sum != r.Bytes || refs != 24 {
			t.Fatalf("%s groups: %+v", by, r)
		}
	}
	review, err := s.StorageReport(ctx, actor, a.ID, "category", "review")
	if err != nil || review.Bytes != 0 || review.DocumentCount != 0 || len(review.Groups) != 0 {
		t.Fatalf("unresolved retention leaked into review: %+v %v", review, err)
	}
	stranger := addAccount(t, owner, "storage-stranger@example.test")
	if _, err = s.StorageReport(ctx, stranger, a.ID, "category", "overview"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign overview: %v", err)
	}
	if _, err = s.StorageReport(ctx, stranger, a.ID, "category", "review"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign review: %v", err)
	}
	if _, err = s.StorageGroupDocuments(ctx, stranger, a.ID, "category", "overview", cat1.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign group drill-down: %v", err)
	}
	if _, err = s.StorageGroupDocuments(ctx, actor, a.ID, "category", "overview", "missing-group", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown group drill-down: %v", err)
	}
	viewer := addAccount(t, owner, "storage-viewer@example.test")
	if _, err = owner.Exec(`INSERT INTO invoyis.archive_memberships(archive_id,account_id,role) VALUES($1,$2,'viewer')`, a.ID, viewer); err != nil {
		t.Fatal(err)
	}
	viewerReport, err := s.StorageReport(ctx, viewer, a.ID, "category", "overview")
	if err != nil {
		t.Fatalf("authorised viewer overview: %v", err)
	}
	if viewerReport.Bytes != 276 || viewerReport.OriginalCount != 23 || viewerReport.DocumentCount != 24 {
		t.Fatalf("viewer should see archive totals including other members’ uploads: %+v", viewerReport)
	}
	if _, err = s.StorageReport(ctx, viewer, a.ID, "category", "review"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nonreviewer candidate access: %v", err)
	}
	if _, err = s.StorageGroupDocuments(ctx, viewer, a.ID, "category", "review", "none", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nonreviewer group candidate access: %v", err)
	}
}
