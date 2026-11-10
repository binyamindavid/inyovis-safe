package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/currency"
)

type TaxLine struct {
	Amount     *string `json:"amount"`
	Currency   string  `json:"currency"`
	Provenance string  `json:"provenance,omitempty"`
}
type ReviewMetadata struct {
	DocumentType     string    `json:"documentType"`
	Supplier         string    `json:"supplier"`
	InvoiceID        string    `json:"invoiceId"`
	AccountReference string    `json:"accountReference"`
	IssueDate        string    `json:"issueDate"`
	Currency         string    `json:"currency"`
	Subtotal         *string   `json:"subtotal"`
	Tax              *string   `json:"tax"`
	Total            *string   `json:"total"`
	TaxLines         []TaxLine `json:"taxLines"`
}
type ReviewWrite struct {
	Action               string         `json:"action"`
	ExpectedVersion      int64          `json:"expectedVersion"`
	IdempotencyKey       string         `json:"idempotencyKey"`
	SourceRunID          *string        `json:"sourceRunId"`
	Metadata             ReviewMetadata `json:"metadata"`
	AcknowledgedWarnings []string       `json:"acknowledgedWarnings"`
}
type Revision struct {
	ID                   string            `json:"id"`
	Kind                 string            `json:"kind"`
	ActorID              int64             `json:"actorId"`
	CreatedAt            time.Time         `json:"createdAt"`
	SourceRunID          *string           `json:"sourceRunId"`
	Metadata             ReviewMetadata    `json:"metadata"`
	Provenance           map[string]string `json:"provenance"`
	AcknowledgedWarnings []string          `json:"acknowledgedWarnings"`
}
type ReviewRead struct {
	DocumentID        string         `json:"documentId"`
	State             string         `json:"state"`
	ReviewVersion     int64          `json:"reviewVersion"`
	SourceRunID       *string        `json:"sourceRunId"`
	Candidates        ReviewMetadata `json:"candidates"`
	CandidateWarnings []string       `json:"candidateWarnings"`
	Confirmed         *Revision      `json:"confirmed"`
	Draft             *Revision      `json:"draft"`
	History           []Revision     `json:"history"`
}

var reviewDecimal = regexp.MustCompile(`^-?[0-9]{1,16}(?:\.[0-9]{1,8})?$`)
var civilDate = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

func cleanText(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return "", ErrInvalid
	}
	for _, r := range s {
		if unicode.IsControl(r) || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return "", ErrInvalid
		}
	}
	return s, nil
}
func validAmount(v *string) bool { return v == nil || reviewDecimal.MatchString(*v) }
func validCurrency(v string) bool {
	if v == "" {
		return true
	}
	if len(v) != 3 || strings.ToUpper(v) != v {
		return false
	}
	_, err := currency.ParseISO(v)
	return err == nil
}
func validateMetadata(m *ReviewMetadata, confirm bool) error {
	var err error
	for _, p := range []*string{&m.Supplier, &m.InvoiceID, &m.AccountReference} {
		*p, err = cleanText(*p)
		if err != nil {
			return err
		}
	}
	if m.DocumentType != "" && m.DocumentType != "invoice" && m.DocumentType != "bill" && m.DocumentType != "credit_note" {
		return ErrInvalid
	}
	if m.IssueDate != "" {
		if !civilDate.MatchString(m.IssueDate) {
			return ErrInvalid
		}
		t, e := time.Parse("2006-01-02", m.IssueDate)
		if e != nil || t.Format("2006-01-02") != m.IssueDate {
			return ErrInvalid
		}
	}
	if !validCurrency(m.Currency) || !validAmount(m.Subtotal) || !validAmount(m.Tax) || !validAmount(m.Total) || len(m.TaxLines) > 32 {
		return ErrInvalid
	}
	for i := range m.TaxLines {
		l := &m.TaxLines[i]
		if l.Amount == nil || !validAmount(l.Amount) || !validCurrency(l.Currency) {
			return ErrInvalid
		}
		l.Provenance = ""
	}
	if confirm && (m.DocumentType == "" || m.Supplier == "" || m.IssueDate == "" || m.Currency == "" || m.Total == nil) {
		return ErrInvalid
	}
	return nil
}
func warningsFor(m ReviewMetadata) []string {
	out := []string{}
	rat := func(p *string) *big.Rat {
		if p == nil {
			return nil
		}
		r, _ := new(big.Rat).SetString(*p)
		return r
	}
	if m.Subtotal != nil && m.Tax != nil && m.Total != nil {
		sum := new(big.Rat).Add(rat(m.Subtotal), rat(m.Tax))
		if sum.Cmp(rat(m.Total)) != 0 {
			out = append(out, "TOTAL_MISMATCH")
		}
	}
	if len(m.TaxLines) > 0 {
		sum := new(big.Rat)
		same := true
		for _, l := range m.TaxLines {
			if l.Currency != "" && l.Currency != m.Currency {
				same = false
			}
			sum.Add(sum, rat(l.Amount))
		}
		if !same {
			out = append(out, "TAX_CURRENCY_MISMATCH")
		} else if m.Tax != nil && sum.Cmp(rat(m.Tax)) != 0 {
			out = append(out, "TAX_LINES_MISMATCH")
		}
	}
	return out
}
func scanRevision(rows interface{ Scan(...any) error }) (Revision, error) {
	var r Revision
	var meta, prov, ack []byte
	var source sql.NullString
	err := rows.Scan(&r.ID, &r.Kind, &r.ActorID, &r.CreatedAt, &source, &meta, &prov, &ack)
	if err != nil {
		return r, err
	}
	if source.Valid {
		r.SourceRunID = &source.String
	}
	if err = json.Unmarshal(meta, &r.Metadata); err != nil {
		return r, err
	}
	if err = json.Unmarshal(prov, &r.Provenance); err != nil {
		return r, err
	}
	err = json.Unmarshal(ack, &r.AcknowledgedWarnings)
	return r, err
}

const revisionColumns = `id,kind,actor_id,created_at,source_extraction_run_id,metadata,provenance,acknowledged_warnings`

func (s *Service) reviewInTx(ctx context.Context, tx *sql.Tx, archiveID, documentID string) (ReviewRead, error) {
	v := ReviewRead{DocumentID: documentID, History: []Revision{}, CandidateWarnings: []string{}}
	var clearance string
	var confirmed, draft sql.NullString
	var digest string
	err := tx.QueryRowContext(ctx, `SELECT d.state,d.clearance,d.review_version,d.current_confirmed_revision_id,d.latest_draft_revision_id,o.sha256 FROM invoyis.documents d JOIN invoyis.submissions sub ON sub.id=d.submission_id JOIN invoyis.stored_objects o ON o.id=sub.stored_object_id WHERE d.archive_id=$1 AND d.id=$2`, archiveID, documentID).Scan(&v.State, &clearance, &v.ReviewVersion, &confirmed, &draft, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if clearance != "cleared" {
		return v, ErrForbidden
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+revisionColumns+` FROM invoyis.document_revisions WHERE archive_id=$1 AND document_id=$2 ORDER BY created_at DESC,id DESC`, archiveID, documentID)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		r, e := scanRevision(rows)
		if e != nil {
			return v, e
		}
		v.History = append(v.History, r)
		if confirmed.Valid && r.ID == confirmed.String {
			copy := r
			v.Confirmed = &copy
		}
		if draft.Valid && r.ID == draft.String {
			copy := r
			v.Draft = &copy
		}
	}
	if err = rows.Err(); err != nil {
		return v, err
	}
	var candidate, warn []byte
	var source string
	err = tx.QueryRowContext(ctx, `SELECT id,candidates,warnings FROM invoyis.extraction_runs WHERE archive_id=$1 AND document_id=$2 AND input_sha256=$3 ORDER BY created_at DESC,id DESC LIMIT 1`, archiveID, documentID, digest).Scan(&source, &candidate, &warn)
	if err == nil {
		v.SourceRunID = &source
		v.Candidates = extractionMetadata(candidate)
		_ = json.Unmarshal(warn, &v.CandidateWarnings)
		for _, field := range []struct {
			name  string
			value *string
		}{
			{"supplier", &v.Candidates.Supplier}, {"invoiceId", &v.Candidates.InvoiceID},
			{"issueDate", &v.Candidates.IssueDate}, {"currency", &v.Candidates.Currency},
		} {
			if _, invalid := cleanText(*field.value); invalid != nil {
				*field.value = ""
				v.CandidateWarnings = append(v.CandidateWarnings, "unsafe_candidate_"+field.name)
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return v, err
	}
	return v, nil
}
func extractionMetadata(raw []byte) ReviewMetadata {
	var source map[string]json.RawMessage
	_ = json.Unmarshal(raw, &source)
	result := ReviewMetadata{TaxLines: []TaxLine{}}
	for key, value := range source {
		var text string
		if key == "taxLines" {
			var amounts []string
			if json.Unmarshal(value, &amounts) == nil {
				for _, amount := range amounts {
					copy := amount
					result.TaxLines = append(result.TaxLines, TaxLine{Amount: &copy})
				}
			}
			continue
		}
		if json.Unmarshal(value, &text) != nil {
			continue
		}
		switch key {
		case "supplier":
			result.Supplier = text
		case "invoiceId":
			result.InvoiceID = text
		case "issueDate":
			if len(text) == 8 && strings.IndexFunc(text, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
				text = text[:4] + "-" + text[4:6] + "-" + text[6:]
			}
			result.IssueDate = text
		case "currency":
			result.Currency = text
		case "subtotal":
			result.Subtotal = &text
		case "tax":
			result.Tax = &text
		case "total":
			result.Total = &text
		}
	}
	return result
}
func (s *Service) ReadReview(ctx context.Context, actor int64, archiveID, documentID string) (ReviewRead, error) {
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) {
		return ReviewRead{}, ErrNotFound
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return ReviewRead{}, e
	}
	defer tx.Rollback()
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "view"); e != nil {
		if errors.Is(e, ErrForbidden) {
			return ReviewRead{}, ErrNotFound
		}
		return ReviewRead{}, e
	}
	v, e := s.reviewInTx(ctx, tx, archiveID, documentID)
	if e != nil {
		return v, e
	}
	e = tx.Commit()
	return v, e
}
func (s *Service) WriteReview(ctx context.Context, actor int64, archiveID, documentID string, w ReviewWrite) (ReviewRead, error) {
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) {
		return ReviewRead{}, ErrNotFound
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return ReviewRead{}, e
	}
	defer tx.Rollback()
	var visible int
	e = tx.QueryRowContext(ctx, `SELECT 1 FROM invoyis.documents WHERE archive_id=$1 AND id=$2`, archiveID, documentID).Scan(&visible)
	if errors.Is(e, sql.ErrNoRows) {
		return ReviewRead{}, ErrNotFound
	}
	if e != nil {
		return ReviewRead{}, e
	}
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "correct"); e != nil {
		return ReviewRead{}, e
	}
	// A row lock serialises confirmation, clearance changes, and duplicate requests.
	var state, clearance, digest string
	var version int64
	var prior sql.NullString
	e = tx.QueryRowContext(ctx, `SELECT d.state,d.clearance,d.review_version,COALESCE(d.latest_draft_revision_id,d.current_confirmed_revision_id),o.sha256 FROM invoyis.documents d JOIN invoyis.submissions sub ON sub.id=d.submission_id JOIN invoyis.stored_objects o ON o.id=sub.stored_object_id WHERE d.archive_id=$1 AND d.id=$2 FOR UPDATE OF d`, archiveID, documentID).Scan(&state, &clearance, &version, &prior, &digest)
	if errors.Is(e, sql.ErrNoRows) {
		return ReviewRead{}, ErrNotFound
	}
	if e != nil {
		return ReviewRead{}, e
	}
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "correct"); e != nil {
		return ReviewRead{}, e
	}
	var membershipLocked int
	e = tx.QueryRowContext(ctx, `SELECT 1 FROM invoyis.archive_memberships WHERE archive_id=$1 AND account_id=$2 FOR SHARE`, archiveID, actor).Scan(&membershipLocked)
	if errors.Is(e, sql.ErrNoRows) {
		return ReviewRead{}, ErrNotFound
	}
	if e != nil {
		return ReviewRead{}, e
	}
	if clearance != "cleared" || state != "needs_review" && state != "ready" {
		return ReviewRead{}, ErrForbidden
	}
	if w.Action != "draft" && w.Action != "confirm" || !uuidPattern.MatchString(w.IdempotencyKey) || w.ExpectedVersion < 0 {
		return ReviewRead{}, ErrInvalid
	}
	if w.SourceRunID != nil && !uuidPattern.MatchString(*w.SourceRunID) {
		return ReviewRead{}, ErrInvalid
	}
	if e := validateMetadata(&w.Metadata, w.Action == "confirm"); e != nil {
		return ReviewRead{}, e
	}
	applicable := warningsFor(w.Metadata)
	sort.Strings(w.AcknowledgedWarnings)
	if w.Action == "confirm" {
		for _, code := range applicable {
			if !contains(w.AcknowledgedWarnings, code) {
				return ReviewRead{}, ErrInvalid
			}
		}
	}
	for _, code := range w.AcknowledgedWarnings {
		if !contains(applicable, code) {
			return ReviewRead{}, ErrInvalid
		}
	}
	fingerprintBody, _ := json.Marshal(w)
	hash := sha256.Sum256(fingerprintBody)
	fingerprint := hex.EncodeToString(hash[:])
	var oldFingerprint string
	var receipt []byte
	e = tx.QueryRowContext(ctx, `SELECT fingerprint,receipt FROM invoyis.review_idempotency WHERE actor_id=$1 AND document_id=$2 AND idempotency_key=$3`, actor, documentID, w.IdempotencyKey).Scan(&oldFingerprint, &receipt)
	if e == nil {
		if oldFingerprint != fingerprint {
			return ReviewRead{}, ErrConflict
		}
		var saved ReviewRead
		if e = json.Unmarshal(receipt, &saved); e != nil {
			return ReviewRead{}, e
		}
		return saved, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return ReviewRead{}, e
	}
	if version != w.ExpectedVersion {
		latest, e := s.reviewInTx(ctx, tx, archiveID, documentID)
		if e != nil {
			return ReviewRead{}, e
		}
		return latest, ErrConflict
	}
	var candidates ReviewMetadata
	if w.SourceRunID != nil {
		var raw []byte
		e = tx.QueryRowContext(ctx, `SELECT candidates FROM invoyis.extraction_runs WHERE id=$1 AND archive_id=$2 AND document_id=$3 AND input_sha256=$4`, *w.SourceRunID, archiveID, documentID, digest).Scan(&raw)
		if errors.Is(e, sql.ErrNoRows) {
			return ReviewRead{}, ErrInvalid
		}
		if e != nil {
			return ReviewRead{}, e
		}
		candidates = extractionMetadata(raw)
	}
	provenance := map[string]string{}
	var priorMeta ReviewMetadata
	var priorProv map[string]string
	if prior.Valid {
		var raw, rawProv []byte
		e = tx.QueryRowContext(ctx, `SELECT metadata,provenance FROM invoyis.document_revisions WHERE id=$1 AND archive_id=$2 AND document_id=$3`, prior.String, archiveID, documentID).Scan(&raw, &rawProv)
		if e != nil {
			return ReviewRead{}, e
		}
		_ = json.Unmarshal(raw, &priorMeta)
		_ = json.Unmarshal(rawProv, &priorProv)
	}
	for i := range w.Metadata.TaxLines {
		line := &w.Metadata.TaxLines[i]
		if i < len(priorMeta.TaxLines) && *line.Amount == *priorMeta.TaxLines[i].Amount && line.Currency == priorMeta.TaxLines[i].Currency && priorMeta.TaxLines[i].Provenance != "" {
			line.Provenance = priorMeta.TaxLines[i].Provenance
		} else if i < len(candidates.TaxLines) && *line.Amount == *candidates.TaxLines[i].Amount && (line.Currency == candidates.TaxLines[i].Currency || candidates.TaxLines[i].Currency == "") {
			line.Provenance = "extracted"
		} else if i < len(candidates.TaxLines) || i < len(priorMeta.TaxLines) {
			line.Provenance = "corrected"
		} else {
			line.Provenance = "manual"
		}
	}
	current, _ := json.Marshal(w.Metadata)
	seed, _ := json.Marshal(candidates)
	previous, _ := json.Marshal(priorMeta)
	var cm, sm, pm map[string]json.RawMessage
	_ = json.Unmarshal(current, &cm)
	_ = json.Unmarshal(seed, &sm)
	_ = json.Unmarshal(previous, &pm)
	for field, value := range cm {
		if string(value) == "null" || string(value) == `""` || string(value) == "[]" {
			continue
		}
		if prior.Valid && string(value) == string(pm[field]) && priorProv[field] != "" {
			provenance[field] = priorProv[field]
		} else if !prior.Valid && string(value) == string(sm[field]) {
			provenance[field] = "extracted"
		} else if string(sm[field]) != "" && string(sm[field]) != "null" {
			provenance[field] = "corrected"
		} else {
			provenance[field] = "manual"
		}
	}
	id, e := newID()
	if e != nil {
		return ReviewRead{}, e
	}
	meta, _ := json.Marshal(w.Metadata)
	prov, _ := json.Marshal(provenance)
	ack, _ := json.Marshal(w.AcknowledgedWarnings)
	var preceding any
	if prior.Valid {
		preceding = prior.String
	}
	kind := w.Action
	if kind == "confirm" {
		kind = "confirmed"
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO invoyis.document_revisions(id,archive_id,document_id,actor_id,kind,preceding_revision_id,source_extraction_run_id,metadata,provenance,acknowledged_warnings,issue_date,subtotal,stated_tax,total) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,'')::date,$12::numeric,$13::numeric,$14::numeric)`, id, archiveID, documentID, actor, kind, preceding, w.SourceRunID, meta, prov, ack, w.Metadata.IssueDate, w.Metadata.Subtotal, w.Metadata.Tax, w.Metadata.Total)
	if e != nil {
		return ReviewRead{}, e
	}
	if w.Action == "confirm" {
		_, e = tx.ExecContext(ctx, `UPDATE invoyis.documents SET current_confirmed_revision_id=$1,latest_draft_revision_id=NULL,review_version=review_version+1,state='ready' WHERE id=$2 AND archive_id=$3`, id, documentID, archiveID)
	} else {
		_, e = tx.ExecContext(ctx, `UPDATE invoyis.documents SET latest_draft_revision_id=$1,review_version=review_version+1 WHERE id=$2 AND archive_id=$3`, id, documentID, archiveID)
	}
	if e != nil {
		return ReviewRead{}, e
	}
	if e = s.audit(ctx, tx, archiveID, actor, "document_review_"+w.Action, nil, map[string]any{"documentId": documentID, "revisionId": id}); e != nil {
		return ReviewRead{}, e
	}
	result, e := s.reviewInTx(ctx, tx, archiveID, documentID)
	if e != nil {
		return ReviewRead{}, e
	}
	receipt, e = json.Marshal(result)
	if e != nil {
		return ReviewRead{}, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO invoyis.review_idempotency(archive_id,document_id,actor_id,idempotency_key,fingerprint,receipt) VALUES($1,$2,$3,$4,$5,$6)`, archiveID, documentID, actor, w.IdempotencyKey, fingerprint, receipt)
	if e != nil {
		return ReviewRead{}, e
	}
	return result, tx.Commit()
}
func contains(a []string, v string) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}
func (s *Service) getReview(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	result, e := s.ReadReview(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"))
	if e != nil {
		writeFailure(w, e, requestID)
		return
	}
	writeJSON(w, 200, requestID, result)
}
func (s *Service) putReview(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var input ReviewWrite
	if e := decodeJSON(w, r, &input); e != nil {
		writeFailure(w, e, requestID)
		return
	}
	result, e := s.WriteReview(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"), input)
	if e != nil {
		if errors.Is(e, ErrConflict) && result.DocumentID != "" {
			writeJSON(w, 409, requestID, map[string]any{"code": "REVIEW_CONFLICT", "latest": result})
			return
		}
		writeFailure(w, e, requestID)
		return
	}
	writeJSON(w, 200, requestID, result)
}
