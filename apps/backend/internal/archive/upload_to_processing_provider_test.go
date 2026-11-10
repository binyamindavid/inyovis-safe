package archive

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/processing"
)

func TestSeaweedUploadThroughProcessing(t *testing.T) {
	if os.Getenv("PROCESSING_PROVIDER_TEST") != "1" {
		t.Skip("real provider opt-in")
	}
	if os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL") == "" || os.Getenv("PROCESSING_PARSER_BINARY") == "" {
		t.Fatal("worker provider configuration missing")
	}
	service, _, owner, admin := setup(t)
	ctx := context.Background()
	storage, err := StorageFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	service.Storage = storage
	archive, err := service.Create(ctx, admin, "personal", "Synthetic processing upload", "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "processing", "testdata", "basic.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.CreateUpload(ctx, admin, archive.ID, claim(data, "processing-provider"))
	if err != nil {
		t.Fatal(err)
	}
	if code := postGrant(t, session.Grant, "", data); code < 200 || code >= 300 {
		t.Fatalf("direct staging POST %d", code)
	}
	receipt, err := service.CompleteUpload(ctx, admin, archive.ID, session.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	var finalKey string
	if err = owner.QueryRow(`SELECT storage_key FROM invoyis.stored_objects WHERE upload_session_id=$1`, session.ID).Scan(&finalKey); err != nil {
		t.Fatal(err)
	}
	original, err := storage.Read(ctx, finalKey, 25<<20)
	if err != nil || !bytes.Equal(original, data) {
		t.Fatal("original changed before processing")
	}
	t.Setenv("S3_ACCESS_KEY_ID", os.Getenv("WORKER_S3_ACCESS_KEY_ID"))
	t.Setenv("S3_SECRET_ACCESS_KEY", os.Getenv("WORKER_S3_SECRET_ACCESS_KEY"))
	workerStorage, err := StorageFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	config := processing.DefaultConfig()
	engine, err := processing.Open(os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL"), workerStorage, func(context.Context, []byte) (bool, error) { return true, nil }, processing.ParseIsolated, config)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	claimed, err := engine.Claim(ctx)
	if err != nil || claimed == nil || claimed.DocumentID != receipt.DocumentID {
		t.Fatalf("claim %v %v", claimed, err)
	}
	if err = engine.Process(ctx, *claimed); err != nil {
		t.Fatal(err)
	}
	var state, clearance, profile, invoiceID string
	err = owner.QueryRow(`SELECT d.state,d.clearance,x.profile,x.candidates->>'invoiceId' FROM invoyis.documents d JOIN invoyis.extraction_runs x ON x.document_id=d.id WHERE d.id=$1`, receipt.DocumentID).Scan(&state, &clearance, &profile, &invoiceID)
	if err != nil || state != "needs_review" || clearance != "cleared" || profile != "basic" || invoiceID != "0000123" {
		t.Fatalf("processing %s %s %s %s %v", state, clearance, profile, invoiceID, err)
	}
	readback, err := workerStorage.Read(ctx, finalKey, 25<<20)
	if err != nil || !bytes.Equal(readback, data) {
		t.Fatal("original changed after processing")
	}
}
