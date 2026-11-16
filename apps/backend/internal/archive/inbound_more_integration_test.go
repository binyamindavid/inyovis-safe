package archive

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
)

func setupInboundTest(t *testing.T) (*Service, *ecdsa.PrivateKey, string, string, int64) {
	t.Helper()
	s, _, owner, admin := setup(t)
	s.Storage = &fakeOriginals{objects: map[string][]byte{}}
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
	archive, err := s.Create(context.Background(), admin, "personal", "Synthetic inbound", "")
	if err != nil {
		t.Fatal(err)
	}
	address, err := s.ChangeForwarding(context.Background(), admin, archive.ID, "enable")
	if err != nil {
		t.Fatal(err)
	}
	other := addAccount(t, owner, "other@example.test")
	if _, err = s.Forwarding(context.Background(), other, archive.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nonmember read address: %v", err)
	}
	if _, err = s.InboundOutcomes(context.Background(), other, archive.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nonmember read outcomes: %v", err)
	}
	return s, private, archive.ID, address.Address, admin
}

func postInbound(t *testing.T, s *Service, private *ecdsa.PrivateKey, contentType string, raw []byte) (int, string) {
	t.Helper()
	response := httptest.NewRecorder()
	s.inboundHTTP(response, inboundRequest(t, private, contentType, raw, fmt.Sprintf("%d", time.Now().Unix())))
	return response.Code, response.Body.String()
}

func TestInboundPermanentResultsAndMixedFiles(t *testing.T) {
	s, private, archiveID, address, admin := setupInboundTest(t)
	envelope := fmt.Sprintf(`{"to":[%q],"from":"sender@example.test"}`, address)
	contentType, raw := signedMultipart(t, "mixed", envelope, []string{"good.pdf", "bad.pdf"})
	bad := bytes.Replace(raw, []byte("%PDF-1.4\nsynthetic-1"), []byte("NOTPDF-1.4\nsynthetic-1"), 1)
	s.BeforeInboundAdmit = func() error { return errors.New("synthetic db outage") }
	if status, _ := postInbound(t, s, private, contentType, bad); status != 500 {
		t.Fatalf("before first write: %d", status)
	}
	s.BeforeInboundAdmit = nil
	if status, body := postInbound(t, s, private, contentType, bad); status != 200 || !strings.Contains(body, "accepted_with_rejections") {
		t.Fatalf("mixed outcome: %d %s", status, body)
	}
	outcomes, err := s.InboundOutcomes(context.Background(), admin, archiveID)
	if err != nil || len(outcomes) != 1 || outcomes[0].AcceptedCount != 1 || outcomes[0].RejectedCount != 1 {
		t.Fatalf("mixed outcomes: %+v %v", outcomes, err)
	}
	portalType, portalRaw := signedMultipart(t, "portal", strings.Replace(envelope, "sender@example.test", "portal@example.test", 1), nil)
	if status, body := postInbound(t, s, private, portalType, portalRaw); status != 200 || !strings.Contains(body, "upload_guidance") {
		t.Fatalf("portal outcome: %d %s", status, body)
	}
	unknownEnvelope := strings.Replace(envelope, address, "aaaaaaaaaaaaaaaa@inbound.invoyis.com", 1)
	unknownType, unknownRaw := signedMultipart(t, "unknown", unknownEnvelope, []string{"test.pdf"})
	if status, body := postInbound(t, s, private, unknownType, unknownRaw); status != 200 || !strings.Contains(body, "unknown_address") {
		t.Fatalf("unknown outcome: %d %s", status, body)
	}
	ambiguousEnvelope := fmt.Sprintf(`{"to":[%q,"aaaaaaaaaaaaaaaa@inbound.invoyis.com"],"from":"sender@example.test"}`, address)
	ambiguousType, ambiguousRaw := signedMultipart(t, "ambiguous", ambiguousEnvelope, []string{"test.pdf"})
	if status, body := postInbound(t, s, private, ambiguousType, ambiguousRaw); status != 200 || !strings.Contains(body, "routing_rejected") {
		t.Fatalf("ambiguous outcome: %d %s", status, body)
	}
	var duplicate bytes.Buffer
	writer := multipart.NewWriter(&duplicate)
	writer.WriteField("envelope", envelope)
	writer.WriteField("text", "Duplicate provider part")
	writer.WriteField("attachment1", "%PDF-1.4\nfirst")
	writer.WriteField("attachment1", "%PDF-1.4\nsecond")
	writer.Close()
	if status, body := postInbound(t, s, private, writer.FormDataContentType(), duplicate.Bytes()); status != 200 || !strings.Contains(body, "attachment_rejected") {
		t.Fatalf("duplicate part outcome: %d %s", status, body)
	}
	var inline bytes.Buffer
	inlineWriter := multipart.NewWriter(&inline)
	inlineWriter.WriteField("envelope", envelope)
	inlineWriter.WriteField("attachment-info", `{"attachment1":{"filename":"inline.pdf","content-id":"<pdf>"}}`)
	for _, file := range []struct {
		key, name, media string
		data             []byte
	}{{"attachment1", "inline.pdf", "application/pdf", []byte("%PDF-1.4\ninline")}, {"attachment2", "signature.png", "image/png", []byte("PNG")}, {"attachment3", "bundle.zip", "application/zip", []byte("PK\x03\x04")}} {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, file.key, file.name))
		header.Set("Content-Type", file.media)
		part, e := inlineWriter.CreatePart(header)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = part.Write(file.data); e != nil {
			t.Fatal(e)
		}
	}
	inlineWriter.Close()
	if status, body := postInbound(t, s, private, inlineWriter.FormDataContentType(), inline.Bytes()); status != 200 || !strings.Contains(body, "accepted") {
		t.Fatalf("inline outcome: %d %s", status, body)
	}
}

func TestInboundConcurrentAdmissionQuota(t *testing.T) {
	s, private, archiveID, address, _ := setupInboundTest(t)
	// The 100th and 101st distinct deliveries race for one remaining slot.
	for i := 0; i < 99; i++ {
		digest := fmt.Sprintf("%064x", i+1)
		var target, result string
		var known bool
		if err := s.DB.QueryRow(`SELECT target_archive,result,known FROM invoyis.inbound_admit($1,$2,true,'')`, digest, strings.Split(address, "@")[0]).Scan(&target, &result, &known); err != nil || target != archiveID || result != "pending" {
			t.Fatalf("seed %d: %s %s %v", i, target, result, err)
		}
		if err := s.DB.QueryRow(`SELECT invoyis.inbound_finish($1,false)`, digest).Scan(&result); err != nil {
			t.Fatal(err)
		}
	}
	envelope := fmt.Sprintf(`{"to":[%q],"from":"sender@example.test"}`, address)
	contentType, raw := signedMultipart(t, "quota", envelope, []string{"one.pdf"})
	raw2 := bytes.Replace(raw, []byte("example@test"), []byte("another@test"), 1)
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for _, body := range [][]byte{raw, raw2} {
		wg.Add(1)
		go func(body []byte) {
			defer wg.Done()
			status, result := postInbound(t, s, private, contentType, body)
			results <- fmt.Sprintf("%d:%s", status, result)
		}(body)
	}
	wg.Wait()
	close(results)
	accepted, limited := 0, 0
	for result := range results {
		if strings.Contains(result, "accepted") {
			accepted++
		}
		if strings.Contains(result, "address_quota") {
			limited++
		}
		if !strings.HasPrefix(result, "200:") {
			t.Fatal(result)
		}
	}
	if accepted != 1 || limited != 1 {
		t.Fatalf("quota results accepted=%d limited=%d", accepted, limited)
	}
}

func TestInboundArchiveQuotaAcrossAddresses(t *testing.T) {
	s, _, owner, admin := setup(t)
	s.Storage = &fakeOriginals{objects: map[string][]byte{}}
	s.InboundPublicKey = &ecdsa.PublicKey{}
	archive, err := s.Create(context.Background(), admin, "personal", "Archive quota", "")
	if err != nil {
		t.Fatal(err)
	}
	address, err := s.ChangeForwarding(context.Background(), admin, archive.ID, "enable")
	if err != nil {
		t.Fatal(err)
	}
	_, err = owner.Exec(`INSERT INTO invoyis.inbound_deliveries(fingerprint,archive_id,address_token,outcome)
 SELECT lpad(to_hex(n),64,'0'),$1,'previous-retired-token','accepted' FROM generate_series(1,500) n`, archive.ID)
	if err != nil {
		t.Fatal(err)
	}
	var target, result string
	var known bool
	err = s.DB.QueryRow(`SELECT target_archive,result,known FROM invoyis.inbound_admit($1,$2,true,'')`, strings.Repeat("f", 64), strings.Split(address.Address, "@")[0]).Scan(&target, &result, &known)
	if err != nil || target != archive.ID || result != "archive_quota" || known {
		t.Fatalf("archive quota: %s %s %t %v", target, result, known, err)
	}
}

func TestInboundConcurrentRetriesPublishOnce(t *testing.T) {
	s, private, archiveID, address, admin := setupInboundTest(t)
	envelope := fmt.Sprintf(`{"to":[%q],"from":"sender@example.test"}`, address)
	contentType, raw := signedMultipart(t, "concurrent-retry", envelope, []string{"same.pdf"})
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); status, _ := postInbound(t, s, private, contentType, raw); results <- status }()
	}
	wg.Wait()
	close(results)
	for status := range results {
		if status != 200 {
			t.Fatalf("concurrent retry status %d", status)
		}
	}
	tx, err := s.transaction(context.Background(), admin, archiveID)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var submissions, originals int
	if err = tx.QueryRow(`SELECT count(*) FROM invoyis.submissions WHERE archive_id=$1`, archiveID).Scan(&submissions); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`SELECT count(*) FROM invoyis.stored_objects WHERE archive_id=$1`, archiveID).Scan(&originals); err != nil {
		t.Fatal(err)
	}
	if submissions != 1 || originals != 1 {
		t.Fatalf("concurrent duplicate: %d submissions %d originals", submissions, originals)
	}
	outcomes, err := s.InboundOutcomes(context.Background(), admin, archiveID)
	if err != nil || len(outcomes) != 1 || outcomes[0].AcceptedCount != 1 {
		t.Fatalf("concurrent outcome: %+v %v", outcomes, err)
	}
}
