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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func inboundRequest(t *testing.T, private *ecdsa.PrivateKey, contentType string, raw []byte, timestamp string) *http.Request {
	t.Helper()
	h := sha256.New()
	h.Write([]byte(timestamp))
	h.Write(raw)
	sig, err := ecdsa.SignASN1(rand.Reader, private, h.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/inbound/sendgrid", bytes.NewReader(raw))
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("X-Twilio-Email-Event-Webhook-Timestamp", timestamp)
	r.Header.Set("X-Twilio-Email-Event-Webhook-Signature", base64.StdEncoding.EncodeToString(sig))
	return r
}

func TestInboundDurableRetryAndRotation(t *testing.T) {
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
	if err := s.ConfigureInbound(base64.StdEncoding.EncodeToString(der), 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	archive, err := s.Create(context.Background(), admin, "personal", "Forwarding synthetic", "")
	if err != nil {
		t.Fatal(err)
	}
	address, err := s.ChangeForwarding(context.Background(), admin, archive.ID, "enable")
	if err != nil {
		t.Fatal(err)
	}
	envelope := fmt.Sprintf(`{"to":[%q],"from":"sender@example.test"}`, address.Address)
	contentType, raw := signedMultipart(t, "retry-one", envelope, []string{"same.pdf", "same.pdf"})
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	post := func(raw []byte, timestamp string) int {
		t.Helper()
		response := httptest.NewRecorder()
		s.inboundHTTP(response, inboundRequest(t, private, contentType, raw, timestamp))
		return response.Code
	}
	invalid := inboundRequest(t, private, contentType, raw, timestamp)
	invalid.Header.Set("X-Twilio-Email-Event-Webhook-Signature", "invalid")
	response := httptest.NewRecorder()
	s.inboundHTTP(response, invalid)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid signature status %d", response.Code)
	}
	var deliveries int
	if err := owner.QueryRow(`SELECT count(*) FROM invoyis.inbound_deliveries`).Scan(&deliveries); err != nil || deliveries != 0 {
		t.Fatalf("invalid signature wrote delivery: %d %v", deliveries, err)
	}

	failOnce := true
	s.AfterInboundPart = func() error {
		if failOnce {
			failOnce = false
			return fmt.Errorf("synthetic outage")
		}
		return nil
	}
	if status := post(raw, timestamp); status != 500 {
		t.Fatalf("partial status %d", status)
	}
	rotated, err := s.ChangeForwarding(context.Background(), admin, archive.ID, "rotate")
	if err != nil || rotated.Address == address.Address {
		t.Fatalf("rotation: %+v %v", rotated, err)
	}
	s.AfterInboundPart = nil
	if status := post(raw, timestamp); status != 200 {
		t.Fatalf("retry after rotation status %d", status)
	}
	if status := post(raw, fmt.Sprintf("%d", time.Now().Add(-48*time.Hour).Unix())); status != 200 {
		t.Fatalf("known late retry status %d", status)
	}
	for table, want := range map[string]int{"inbound_deliveries": 1, "inbound_parts": 2, "submissions": 2, "stored_objects": 2, "processing_jobs": 2} {
		var count int
		if err := owner.QueryRow(fmt.Sprintf(`SELECT count(*) FROM invoyis.%s`, table)).Scan(&count); err != nil || count != want {
			t.Fatalf("%s: %d %v", table, count, err)
		}
	}
	// A separate forward with a new stable header creates another submission,
	// while the original attachment remains archive-scoped and shared.
	second := bytes.Replace(raw, []byte("example@test"), []byte("second@test"), 1)
	if status := post(second, timestamp); status != 200 {
		t.Fatalf("second forward status %d", status)
	}
	var submissions, originals int
	owner.QueryRow(`SELECT count(*) FROM invoyis.submissions`).Scan(&submissions)
	owner.QueryRow(`SELECT count(*) FROM invoyis.stored_objects`).Scan(&originals)
	if submissions != 2 || originals != 2 {
		t.Fatalf("rotated token accepted new delivery: %d submissions %d originals", submissions, originals)
	}
	// The retired token is rejected for a new delivery. Use the new address for
	// a deliberate second forward of the same PDFs.
	newEnvelope := strings.ReplaceAll(envelope, address.Address, rotated.Address)
	newContent, newRaw := signedMultipart(t, "new-delivery", newEnvelope, []string{"same.pdf", "same.pdf"})
	contentType = newContent
	if status := post(newRaw, timestamp); status != 200 {
		t.Fatalf("new forward status %d", status)
	}
	owner.QueryRow(`SELECT count(*) FROM invoyis.submissions`).Scan(&submissions)
	owner.QueryRow(`SELECT count(*) FROM invoyis.stored_objects`).Scan(&originals)
	if submissions != 4 || originals != 2 {
		t.Fatalf("dedup: %d submissions %d originals", submissions, originals)
	}
	secondForward := bytes.Replace(newRaw, []byte("example@test"), []byte("second@test"), 1)
	if status := post(secondForward, timestamp); status != 200 {
		t.Fatalf("second same-address forward status %d", status)
	}
	owner.QueryRow(`SELECT count(*) FROM invoyis.submissions`).Scan(&submissions)
	owner.QueryRow(`SELECT count(*) FROM invoyis.stored_objects`).Scan(&originals)
	if submissions != 6 || originals != 2 {
		t.Fatalf("separate forwards: %d submissions %d originals", submissions, originals)
	}
}
