package archive

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"testing"
)

func signedMultipart(t *testing.T, boundary string, envelope string, filenames []string) (string, []byte) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary(boundary); err != nil {
		t.Fatal(err)
	}
	for _, field := range []struct{ name, value string }{{"envelope", envelope}, {"headers", "Message-ID: <example@test>\r\nDate: Tue, 1 Jan 2026 10:00:00 +0000"}, {"to", "display-only@example.test"}, {"text", "Please see attachment"}} {
		if err := writer.WriteField(field.name, field.value); err != nil {
			t.Fatal(err)
		}
	}
	for i, filename := range filenames {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="attachment`+string(rune('1'+i))+`"; filename="`+filename+`"`)
		header.Set("Content-Type", "application/pdf")
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write([]byte(fmt.Sprintf("%%PDF-1.4\nsynthetic-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return writer.FormDataContentType(), body.Bytes()
}

func TestInboundSignatureUsesExactRawMultipart(t *testing.T) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	public, err := parseInboundPublicKey(base64.StdEncoding.EncodeToString(der))
	if err != nil {
		t.Fatal(err)
	}
	_, raw := signedMultipart(t, "boundary-one", `{"to":["M4X7Q2V6N3JK5WZP@INBOUND.INVOYIS.COM"],"from":"sender@example.test"}`, []string{"invoice.pdf"})
	timestamp := "1767225600"
	digest := sha256.Sum256(append([]byte(timestamp), raw...))
	sig, err := ecdsa.SignASN1(rand.Reader, private, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(sig)
	if _, ok := verifyInboundSignature(public, timestamp, encoded, raw); !ok {
		t.Fatal("signed body rejected")
	}
	changed := bytes.Clone(raw)
	changed[len(changed)-10] ^= 1
	if _, ok := verifyInboundSignature(public, timestamp, encoded, changed); ok {
		t.Fatal("modified body accepted")
	}
	if _, ok := verifyInboundSignature(public, timestamp+"0", encoded, raw); ok {
		t.Fatal("modified timestamp accepted")
	}
}

func TestInboundRouteAndStableFingerprint(t *testing.T) {
	envelope := `{"to":["M4X7Q2V6N3JK5WZP@INBOUND.INVOYIS.COM","m4x7q2v6n3jk5wzp@inbound.invoyis.com","info@invoyis.com"],"from":"sender@example.test"}`
	contentA, rawA := signedMultipart(t, "first-boundary", envelope, []string{"same.pdf", "same.pdf"})
	contentB, rawB := signedMultipart(t, "second-boundary", envelope, []string{"same.pdf", "same.pdf"})
	a, err := parseInboundMultipart(contentA, rawA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseInboundMultipart(contentB, rawB)
	if err != nil {
		t.Fatal(err)
	}
	if a.Address != "m4x7q2v6n3jk5wzp@inbound.invoyis.com" || len(a.Parts) != 2 {
		t.Fatalf("unexpected route or parts: %+v", a)
	}
	if a.Fingerprint != b.Fingerprint {
		t.Fatal("multipart boundary changed delivery identity")
	}
	for _, bad := range []string{
		`{"to":["m4x7q2v6n3jk5wzp+tag@inbound.invoyis.com"],"from":"sender@example.test"}`,
		`{"to":["m4x7q2v6n3jk5wzp@inbound.invoyis.com","aaaaaaaaaaaaaaaa@inbound.invoyis.com"],"from":"sender@example.test"}`,
		`{"to":[],"from":"sender@example.test"}`,
		`{"to":["m4x7q2v6n3jk5wzp@inbound.invoyis.com"],"from":"bad address"}`,
	} {
		if _, err := routeInboundEnvelope(bad); err == nil {
			t.Fatalf("accepted invalid envelope %s", bad)
		}
	}
}

func TestInboundDuplicatePartKeysRejected(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("envelope", `{"to":["m4x7q2v6n3jk5wzp@inbound.invoyis.com"],"from":"sender@example.test"}`)
	for range 2 {
		writer.WriteField("attachment1", "%PDF-1.4")
	}
	writer.Close()
	if _, err := parseInboundMultipart(writer.FormDataContentType(), body.Bytes()); err == nil {
		t.Fatal("duplicate part key accepted")
	}
}

func TestInboundApplicationAttachmentCaps(t *testing.T) {
	envelope := `{"to":["m4x7q2v6n3jk5wzp@inbound.invoyis.com"],"from":"sender@example.test"}`
	for _, fixture := range []struct {
		name  string
		count int
		size  int
	}{{"count", 11, 16}, {"aggregate", 2, 26 << 20}} {
		t.Run(fixture.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			writer.WriteField("envelope", envelope)
			data := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("x"), fixture.size)...)
			for i := 1; i <= fixture.count; i++ {
				part, err := writer.CreateFormFile(fmt.Sprintf("attachment%d", i), "file.pdf")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = part.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			writer.Close()
			if _, err := parseInboundMultipart(writer.FormDataContentType(), body.Bytes()); err == nil {
				t.Fatal("application attachment cap not enforced")
			}
		})
	}
}
