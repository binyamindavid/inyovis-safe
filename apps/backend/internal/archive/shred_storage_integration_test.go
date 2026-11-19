package archive

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/minio/minio-go/v7"
)

func TestRealObjectStorageShredVersionsAndExactKey(t *testing.T) {
	if os.Getenv("SHRED_OBJECT_STORAGE_TEST") != "1" {
		t.Skip("real object storage test not enabled")
	}
	store, e := StorageFromEnv()
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	suffix, _ := newID()
	store.bucket = "invoyis-shred-test-" + suffix[:8]
	if e = store.client.MakeBucket(ctx, store.bucket, minio.MakeBucketOptions{}); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = store.client.RemoveBucket(context.Background(), store.bucket) })
	versioned := store.client.SetBucketVersioning(ctx, store.bucket, minio.BucketVersioningConfiguration{Status: minio.Enabled}) == nil
	key := "originals/" + suffix
	neighbor := key + "-keep"
	put := func(k, v string) {
		t.Helper()
		_, e := store.client.PutObject(ctx, store.bucket, k, bytes.NewReader([]byte(v)), int64(len(v)), minio.PutObjectOptions{})
		if e != nil {
			t.Fatal(e)
		}
	}
	put(key, "first")
	if versioned {
		put(key, "second")
		versions := 0
		for object := range store.client.ListObjects(ctx, store.bucket, minio.ListObjectsOptions{Prefix: key, Recursive: true, WithVersions: true}) {
			if object.Err != nil {
				t.Fatal(object.Err)
			}
			if object.Key == key {
				versions++
			}
		}
		if versions < 2 {
			t.Fatalf("versioning enabled but only %d versions found", versions)
		}
	}
	put(neighbor, "keep")
	exists, e := store.OriginalExists(ctx, key)
	if e != nil || !exists {
		t.Fatalf("before delete: %t %v", exists, e)
	}
	if e = store.DeleteOriginal(ctx, key); e != nil {
		t.Fatal(e)
	}
	exists, e = store.OriginalExists(ctx, key)
	if e != nil || exists {
		t.Fatalf("version remains: %t %v", exists, e)
	}
	exists, e = store.OriginalExists(ctx, neighbor)
	if e != nil || !exists {
		t.Fatalf("neighbor removed: %t %v", exists, e)
	}
	if e = store.DeleteOriginal(ctx, neighbor); e != nil {
		t.Fatal(e)
	}
	if !versioned {
		t.Log("object store did not enable versioning; unversioned exact-key deletion verified")
	}
}

func TestRealStorageDocumentLifecycle(t *testing.T) {
	if os.Getenv("SHRED_OBJECT_STORAGE_TEST") != "1" {
		t.Skip("real object storage test not enabled")
	}
	s, _, owner, admin := setup(t)
	ctx := context.Background()
	store, e := StorageFromEnv()
	if e != nil {
		t.Fatal(e)
	}
	suffix, _ := newID()
	store.bucket = "invoyis-shred-test-" + suffix[:8]
	if e = store.client.MakeBucket(ctx, store.bucket, minio.MakeBucketOptions{}); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = store.client.RemoveBucket(context.Background(), store.bucket) })
	s.Storage = store
	a, e := s.Create(ctx, admin, "personal", "Real storage disposal", "")
	if e != nil {
		t.Fatal(e)
	}
	data := []byte("%PDF-1.7\n% real storage shred\n%%EOF")
	session, e := s.CreateUpload(ctx, admin, a.ID, claim(data, "real-storage-shred"))
	if e != nil {
		t.Fatal(e)
	}
	if e = store.PutOriginal(ctx, session.Grant.Fields["key"], data); e != nil {
		t.Fatal(e)
	}
	receipt, e := s.CompleteUpload(ctx, admin, a.ID, session.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.documents SET state='needs_review',clearance='cleared' WHERE id=$1`, receipt.DocumentID); e != nil {
		t.Fatal(e)
	}
	_, e = write(t, s, admin, a.ID, receipt.DocumentID, "confirm", 0, ReviewMetadata{DocumentType: TypeLease, Title: "Real storage", IssueDate: "2020-01-01"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.processing_jobs SET state='succeeded' WHERE document_id=$1`, receipt.DocumentID); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(`UPDATE invoyis.upload_sessions SET grant_expires_at=now()-interval '1 second',finalise_by=now()-interval '1 second' WHERE id=$1`, session.ID); e != nil {
		t.Fatal(e)
	}
	status, e := s.DocumentStatus(ctx, admin, a.ID, receipt.DocumentID)
	if e != nil {
		t.Fatal(e)
	}
	result, e := s.AuthorizeShred(ctx, admin, a.ID, receipt.DocumentID, status.OriginalFilename)
	if e != nil || result.Status != "pending" {
		t.Fatalf("authorize %+v %v", result, e)
	}
	for i := 0; i < 8; i++ {
		found, runErr := s.ReconcileOneShred(ctx)
		if runErr != nil {
			t.Fatal(runErr)
		}
		if !found {
			break
		}
	}
	result, e = s.ShredEligibility(ctx, admin, a.ID, receipt.DocumentID)
	if e != nil || result.Status != "verified" {
		t.Fatalf("verified %+v %v", result, e)
	}
	for object := range store.client.ListObjects(ctx, store.bucket, minio.ListObjectsOptions{Recursive: true, WithVersions: true}) {
		if object.Err != nil {
			t.Fatal(object.Err)
		}
		t.Fatalf("recoverable object remains: %s", object.Key)
	}
}
