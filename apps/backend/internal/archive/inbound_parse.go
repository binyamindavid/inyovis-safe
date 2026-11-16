package archive

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const inboundBodyCap = 64 << 20 // 50 MiB of files plus multipart overhead.
const inboundAggregateCap = 50 << 20
const inboundFileCountCap = 10

var inboundPartKey = regexp.MustCompile(`^attachment[1-9][0-9]*$`)
var inboundAddress = regexp.MustCompile(`^[a-z2-7]{16}@inbound\.invoyis\.com$`)
var inboundMailbox = regexp.MustCompile(`^[\x21-\x3f\x41-\x7e]+@[\x21-\x3f\x41-\x7e]+$`)

type inboundPart struct {
	Key, Filename, MediaType, Disposition, SHA256 string
	Data                                          []byte
}

type inboundMessage struct {
	Address, Fingerprint string
	Parts                []inboundPart
	BodyOnly             bool
}

// SendGrid's policy key is a Base64-encoded DER SubjectPublicKeyInfo value.
func parseInboundPublicKey(encoded string) (*ecdsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	public, ok := key.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("inbound policy key is not ECDSA")
	}
	return public, nil
}

func (s *Service) ConfigureInbound(encodedKey string, freshness time.Duration) error {
	key, err := parseInboundPublicKey(encodedKey)
	if err != nil {
		return err
	}
	if freshness < time.Hour || freshness > 30*24*time.Hour {
		return ErrInvalid
	}
	s.InboundPublicKey = key
	s.InboundFreshness = freshness
	return nil
}

// Verify before any multipart decoding. The timestamp's original bytes are signed.
func verifyInboundSignature(key *ecdsa.PublicKey, timestamp, signature string, raw []byte) (time.Time, bool) {
	if key == nil || timestamp == "" || signature == "" {
		return time.Time{}, false
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}, false
	}
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return time.Time{}, false
	}
	h := sha256.New()
	h.Write([]byte(timestamp))
	h.Write(raw)
	if !ecdsa.VerifyASN1(key, h.Sum(nil), sig) {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0).UTC(), true
}

func routeInboundEnvelope(value string) (string, error) {
	var envelope struct {
		To   []string `json:"to"`
		From string   `json:"from"`
	}
	if json.Unmarshal([]byte(value), &envelope) != nil || len(envelope.To) == 0 || len(envelope.To) > 100 || !inboundMailbox.MatchString(envelope.From) {
		return "", ErrInvalid
	}
	unique := map[string]bool{}
	for _, recipient := range envelope.To {
		if !inboundMailbox.MatchString(recipient) {
			return "", ErrInvalid
		}
		recipient = strings.ToLower(recipient)
		local, domain, _ := strings.Cut(recipient, "@")
		if domain != forwardingDomain {
			continue
		}
		if strings.Contains(local, "+") || !inboundAddress.MatchString(recipient) {
			return "", ErrInvalid
		}
		unique[recipient] = true
	}
	if len(unique) != 1 {
		return "", ErrInvalid
	}
	for address := range unique {
		return address, nil
	}
	return "", ErrInvalid
}

func canonicalEnvelope(value string) string {
	var envelope struct {
		To   []string `json:"to"`
		From string   `json:"from"`
	}
	if json.Unmarshal([]byte(value), &envelope) != nil {
		return value
	}
	for i := range envelope.To {
		envelope.To[i] = strings.ToLower(envelope.To[i])
	}
	sort.Strings(envelope.To)
	to := envelope.To[:0]
	for _, recipient := range envelope.To {
		if len(to) == 0 || to[len(to)-1] != recipient {
			to = append(to, recipient)
		}
	}
	envelope.To = to
	envelope.From = strings.ToLower(envelope.From)
	canonical, _ := json.Marshal(envelope)
	return string(canonical)
}

func fingerprintField(h io.Writer, value string) {
	_, _ = fmt.Fprintf(h, "%d:", len(value))
	_, _ = io.WriteString(h, value)
}

// The fingerprint deliberately excludes HTTP multipart boundaries and signature
// metadata. Identical parsed deliveries can still be indistinguishable: live
// SendGrid retries and separate forwards must be measured before pilot use.
func parseInboundMultipart(contentType string, raw []byte) (inboundMessage, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return inboundMessage{}, ErrInvalid
	}
	reader := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	fields := map[string]string{}
	seen := map[string]bool{}
	var files []inboundPart
	var aggregate int64
	malformed := false
	for {
		part, e := reader.NextPart()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return inboundMessage{}, ErrInvalid
		}
		key := part.FormName()
		if key == "" || seen[key] {
			malformed = true
		}
		seen[key] = true
		if len(seen) > 128 {
			part.Close()
			return inboundMessage{}, ErrInvalid
		}
		value, e := io.ReadAll(io.LimitReader(part, inboundBodyCap+1))
		part.Close()
		if e != nil || len(value) > inboundBodyCap {
			return inboundMessage{}, ErrInvalid
		}
		if inboundPartKey.MatchString(key) {
			if len(files) >= inboundFileCountCap {
				malformed = true
			}
			aggregate += int64(len(value))
			if aggregate > inboundAggregateCap {
				malformed = true
			}
			digest := sha256.Sum256(value)
			files = append(files, inboundPart{Key: key, Filename: part.FileName(), MediaType: part.Header.Get("Content-Type"), Disposition: part.Header.Get("Content-Disposition"), SHA256: hex.EncodeToString(digest[:]), Data: value})
		} else if key == "envelope" || key == "headers" || key == "text" || key == "html" || key == "attachment-info" || key == "subject" || key == "from" || key == "to" || key == "charsets" || key == "spam_score" || key == "spam_report" {
			if len(value) > 1<<20 {
				return inboundMessage{}, ErrInvalid
			}
			fields[key] = string(value)
		} else if part.FileName() != "" || strings.HasPrefix(key, "attachment") {
			malformed = true
		}
	}
	address, err := routeInboundEnvelope(fields["envelope"])
	if err != nil {
		malformed = true
	}
	// Stable provider part keys determine ordering; duplicate filenames remain valid.
	sort.Slice(files, func(i, j int) bool { return files[i].Key < files[j].Key })
	h := sha256.New()
	fingerprintField(h, canonicalEnvelope(fields["envelope"]))
	for _, name := range []string{"headers", "text", "html", "subject", "from", "to"} {
		fingerprintField(h, fields[name])
	}
	for _, file := range files {
		fingerprintField(h, file.Key)
		fingerprintField(h, file.SHA256)
	}
	message := inboundMessage{Address: address, Fingerprint: hex.EncodeToString(h.Sum(nil)), Parts: files, BodyOnly: strings.TrimSpace(fields["text"]) != "" || strings.TrimSpace(fields["html"]) != ""}
	if malformed {
		return message, ErrInvalid
	}
	return message, nil
}

func inboundRawBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.ContentLength > inboundBodyCap {
		return nil, errors.New("body too large")
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, inboundBodyCap))
	if err != nil {
		return nil, err
	}
	return data, nil
}
