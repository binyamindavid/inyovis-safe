package archive

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/processing"
)

func TestInboundWorkerQuarantineAndExactDownload(t *testing.T) {
	workerURL := os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL")
	if workerURL == "" {
		t.Skip("disposable worker database URL required")
	}
	s, _, owner, admin := setup(t)
	storage := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = storage
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigureInbound(base64.StdEncoding.EncodeToString(der), 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	archive, err := s.Create(context.Background(), admin, "personal", "Worker intake", "")
	if err != nil {
		t.Fatal(err)
	}
	address, err := s.ChangeForwarding(context.Background(), admin, archive.ID, "enable")
	if err != nil {
		t.Fatal(err)
	}
	envelope := fmt.Sprintf(`{"to":[%q],"from":"sender@example.test"}`, address.Address)
	contentType, raw := signedMultipart(t, "blocked-worker", envelope, []string{"blocked.pdf"})
	status, _ := postInbound(t, s, private, contentType, raw)
	if status != http.StatusOK {
		t.Fatalf("blocked intake %d", status)
	}
	var blockedID string
	if err = owner.QueryRow(`SELECT id FROM invoyis.documents WHERE archive_id=$1`, archive.ID).Scan(&blockedID); err != nil {
		t.Fatal(err)
	}
	parser := func(context.Context, []byte) (processing.Result, error) {
		return processing.Result{Candidates: map[string]any{}, Provenance: map[string]any{}, Warnings: []string{"no_supported_xml"}}, nil
	}
	blockedWorker, err := processing.Open(workerURL, storage, func(context.Context, []byte) (bool, error) { return false, nil }, parser, processing.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	claim, err := blockedWorker.Claim(context.Background())
	if err != nil || claim == nil || claim.DocumentID != blockedID {
		t.Fatalf("blocked claim: %+v %v", claim, err)
	}
	if err = blockedWorker.Process(context.Background(), *claim); err != nil {
		t.Fatal(err)
	}
	blockedWorker.Close()
	var clearance string
	if err = owner.QueryRow(`SELECT clearance FROM invoyis.documents WHERE id=$1`, blockedID).Scan(&clearance); err != nil || clearance != "blocked" {
		t.Fatalf("blocked clearance %s %v", clearance, err)
	}
	if _, _, err = s.DownloadOriginal(context.Background(), admin, archive.ID, blockedID); err == nil {
		t.Fatal("blocked original downloaded")
	}

	clearRaw := bytes.Replace(raw, []byte("%PDF-1.4\nsynthetic-0"), []byte("%PDF-1.4\nclear-unique"), 1)
	clearRaw = bytes.Replace(clearRaw, []byte("example@test"), []byte("clear@test"), 1)
	status, _ = postInbound(t, s, private, contentType, clearRaw)
	if status != http.StatusOK {
		t.Fatalf("clear intake %d", status)
	}
	var clearID, source string
	if err = owner.QueryRow(`SELECT d.id,s.source FROM invoyis.documents d JOIN invoyis.submissions s ON s.id=d.submission_id WHERE d.archive_id=$1 AND d.id<>$2`, archive.ID, blockedID).Scan(&clearID, &source); err != nil || source != "forwarded_email" {
		t.Fatalf("source %s %v", source, err)
	}
	clearWorker, err := processing.Open(workerURL, storage, func(context.Context, []byte) (bool, error) { return true, nil }, parser, processing.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer clearWorker.Close()
	claim, err = clearWorker.Claim(context.Background())
	if err != nil || claim == nil || claim.DocumentID != clearID {
		t.Fatalf("clear claim: %+v %v", claim, err)
	}
	if err = clearWorker.Process(context.Background(), *claim); err != nil {
		t.Fatal(err)
	}
	data, _, err := s.DownloadOriginal(context.Background(), admin, archive.ID, clearID)
	if err != nil {
		t.Fatal(err)
	}
	expected := []byte("%PDF-1.4\nclear-unique")
	actualHash := sha256.Sum256(data)
	expectedHash := sha256.Sum256(expected)
	if !bytes.Equal(data, expected) || hex.EncodeToString(actualHash[:]) != hex.EncodeToString(expectedHash[:]) {
		t.Fatal("download differs from source SHA-256")
	}
	if strings.Contains(string(data), "synthetic-0") {
		t.Fatal("original changed")
	}
}
