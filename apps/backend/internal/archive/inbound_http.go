package archive

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

func (s *Service) inboundHTTP(w http.ResponseWriter, r *http.Request) {
	// No session is involved: possession of an address authorises submission only.
	// The configured policy key is mandatory; a missing key fails closed.
	if s.InboundPublicKey == nil {
		http.Error(w, "webhook unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.Storage == nil {
		http.Error(w, "intake unavailable", http.StatusServiceUnavailable)
		return
	}
	raw, err := inboundRawBody(w, r)
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) || r.ContentLength > inboundBodyCap {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "request read failed", http.StatusInternalServerError)
		}
		return
	}
	timestamp, valid := verifyInboundSignature(s.InboundPublicKey,
		r.Header.Get("X-Twilio-Email-Event-Webhook-Timestamp"),
		r.Header.Get("X-Twilio-Email-Event-Webhook-Signature"), raw)
	if !valid {
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	message, parseErr := parseInboundMultipart(r.Header.Get("Content-Type"), raw)
	rejection := ""
	if parseErr != nil {
		// A signed malformed multipart still has a terminal, durable disposition.
		// Raw-byte identity is a fallback only when canonical fields are unavailable.
		if message.Fingerprint == "" {
			sum := sha256.Sum256(raw)
			message.Fingerprint = hex.EncodeToString(sum[:])
		}
		if message.Address != "" {
			rejection = "attachment_rejected"
		} else {
			rejection = "routing_rejected"
		}
	}
	if s.BeforeInboundAdmit != nil {
		if err := s.BeforeInboundAdmit(); err != nil {
			http.Error(w, "intake unavailable", http.StatusInternalServerError)
			return
		}
	}
	freshness := s.InboundFreshness
	if freshness <= 0 {
		freshness = 72 * time.Hour
	}
	age := s.Now().Sub(timestamp)
	fresh := age <= freshness && age >= -5*time.Minute
	token := strings.TrimSuffix(message.Address, "@"+forwardingDomain)
	var archiveID sql.NullString
	var outcome string
	var known bool
	err = s.DB.QueryRowContext(r.Context(), `SELECT target_archive,result,known FROM invoyis.inbound_admit($1,$2,$3,$4)`, message.Fingerprint, token, fresh, rejection).Scan(&archiveID, &outcome, &known)
	if err != nil {
		http.Error(w, "intake unavailable", http.StatusInternalServerError)
		return
	}
	if outcome == "stale" {
		http.Error(w, "stale webhook", http.StatusUnauthorized)
		return
	}
	if outcome != "pending" {
		writeJSON(w, http.StatusOK, "", map[string]any{"outcome": outcome})
		return
	}
	if !archiveID.Valid {
		http.Error(w, "intake unavailable", http.StatusInternalServerError)
		return
	}
	for _, part := range message.Parts {
		if err := s.intakeInboundPart(r, message.Fingerprint, part); err != nil {
			http.Error(w, "intake unavailable", http.StatusInternalServerError)
			return
		}
		if s.AfterInboundPart != nil {
			if err := s.AfterInboundPart(); err != nil {
				http.Error(w, "intake unavailable", http.StatusInternalServerError)
				return
			}
		}
	}
	err = s.DB.QueryRowContext(r.Context(), `SELECT invoyis.inbound_finish($1,$2)`, message.Fingerprint, message.BodyOnly).Scan(&outcome)
	if err != nil {
		http.Error(w, "intake unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, "", map[string]any{"outcome": outcome})
}

func (s *Service) intakeInboundPart(r *http.Request, digest string, part inboundPart) error {
	filename := part.Filename
	if filename == "" {
		filename = part.Key + ".pdf"
	}
	if strings.ContainsAny(filename, "\x00/\\") || len(filename) > 255 {
		filename = part.Key + ".pdf"
	}
	// Ignore common inline decoration and archives; never fetch links or unpack ZIPs.
	if len(part.Data) < 5 || string(part.Data[:5]) != "%PDF-" {
		pdfClaim := strings.HasSuffix(strings.ToLower(part.Filename), ".pdf") || strings.HasPrefix(strings.ToLower(part.MediaType), "application/pdf")
		if !pdfClaim && (strings.Contains(strings.ToLower(part.Disposition), "inline") || strings.Contains(strings.ToLower(part.MediaType), "image/") || strings.Contains(strings.ToLower(part.MediaType), "zip") || strings.HasSuffix(strings.ToLower(part.Filename), ".zip")) {
			return nil
		}
	}
	rejection := ""
	row := uploadRow{declaredBytes: int64(len(part.Data)), declaredSHA: part.SHA256}
	if validateStaged(part.Data, row, s.uploadLimit()) != nil {
		rejection = "invalid_pdf"
	}
	var state string
	err := s.DB.QueryRowContext(r.Context(), `SELECT invoyis.inbound_part_prepare($1,$2,$3,$4,$5,$6)`, digest, part.Key, filename, len(part.Data), part.SHA256, rejection).Scan(&state)
	if err != nil || state == "accepted" || state == "rejected" {
		return err
	}
	key := "inbound/" + digest + "/" + part.Key
	if err = s.ensureVerifiedObject(r.Context(), key, part.Data, part.SHA256); err != nil {
		return err
	}
	var canonicalKey string
	var canonicalBytes int64
	err = s.DB.QueryRowContext(r.Context(), `SELECT storage_key,byte_count FROM invoyis.inbound_canonical($1,$2)`, digest, part.SHA256).Scan(&canonicalKey, &canonicalBytes)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		original, readErr := s.Storage.Read(r.Context(), canonicalKey, s.uploadLimit())
		if readErr != nil {
			return readErr
		}
		if int64(len(original)) != canonicalBytes || !sameDigest(original, part.SHA256) {
			return ErrIntegrity
		}
	}
	var documentID string
	return s.DB.QueryRowContext(r.Context(), `SELECT invoyis.inbound_part_publish($1,$2,$3)`, digest, part.Key, key).Scan(&documentID)
}
