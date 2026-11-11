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
	// Fields below belong to non-invoice documents (lease, court correspondence, other).
	Title     string `json:"title"`
	Sender    string `json:"sender"`
	Reference string `json:"reference"`
	Notes     string `json:"notes"`
}

const (
	TypeLease               = "lease"
	TypeCourtCorrespondence = "court_correspondence"
	TypeOther               = "other"
)

// IsInvoiceType reports whether the type uses the invoice field group.
func IsInvoiceType(t string) bool { return t == "invoice" || t == "bill" || t == "credit_note" }

// IsDocumentType reports whether the type uses the generic (non-invoice) field group.
func IsDocumentType(t string) bool {
	return t == TypeLease || t == TypeCourtCorrespondence || t == TypeOther
}

// MarshalJSON emits only the field group that applies to the document type, so a
// non-invoice revision never carries invoice keys and an invoice never carries
// document keys. An undecided draft (empty type) emits both groups.
func (m ReviewMetadata) MarshalJSON() ([]byte, error) {
	switch {
	case IsDocumentType(m.DocumentType):
		return json.Marshal(struct {
			DocumentType string `json:"documentType"`
			Title        string `json:"title"`
			Sender       string `json:"sender"`
			IssueDate    string `json:"issueDate"`
			Reference    string `json:"reference"`
			Notes        string `json:"notes"`
		}{m.DocumentType, m.Title, m.Sender, m.IssueDate, m.Reference, m.Notes})
	case IsInvoiceType(m.DocumentType), m.DocumentType == "" && m.Title == "" && m.Sender == "" && m.Reference == "" && m.Notes == "":
		// An undecided draft with no document-group values keeps the pre-#35 shape, so the
		// idempotency fingerprint of a draft saved before this change is unchanged.
		lines := m.TaxLines
		if lines == nil {
			lines = []TaxLine{}
		}
		return json.Marshal(struct {
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
		}{m.DocumentType, m.Supplier, m.InvoiceID, m.AccountReference, m.IssueDate, m.Currency, m.Subtotal, m.Tax, m.Total, lines})
	}
	type plain ReviewMetadata
	if m.TaxLines == nil {
		m.TaxLines = []TaxLine{}
	}
	return json.Marshal(plain(m))
}

type ReviewWrite struct {
	Action               string            `json:"action"`
	ExpectedVersion      int64             `json:"expectedVersion"`
	IdempotencyKey       string            `json:"idempotencyKey"`
	SourceRunID          *string           `json:"sourceRunId"`
	Metadata             ReviewMetadata    `json:"metadata"`
	AcknowledgedWarnings []string          `json:"acknowledgedWarnings"`
	InputLocale          string            `json:"inputLocale,omitempty"`
	RawAmounts           *ReviewRawAmounts `json:"rawAmounts,omitempty"`
}
type ReviewRawAmounts struct {
	Subtotal *string  `json:"subtotal"`
	Tax      *string  `json:"tax"`
	Total    *string  `json:"total"`
	TaxLines []string `json:"taxLines"`
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

func cleanText(s string) (string, error) { return cleanBounded(s, 400, false) }

// cleanBounded trims s, enforces a byte limit and rejects control and bidirectional
// override characters. Multiline fields may contain newlines and tabs.
func cleanBounded(s string, limit int, multiline bool) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > limit {
		return "", ErrInvalid
	}
	for _, r := range s {
		if multiline && (r == '\n' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return "", ErrInvalid
		}
	}
	return s, nil
}
func validAmount(v *string) bool { return v == nil || reviewDecimal.MatchString(*v) }
func parseLocalizedReviewDecimal(raw, locale string) *string {
	v := strings.TrimSpace(strings.Replace(raw, "−", "-", 1))
	if v == "" {
		return nil
	}
	sign := ""
	if strings.HasPrefix(v, "-") {
		sign, v = "-", v[1:]
	}
	decimal, group := ".", ","
	if locale == "fr" {
		decimal, group = ",", " "
	}
	parts := strings.Split(v, decimal)
	if len(parts) > 2 {
		return nil
	}
	whole := parts[0]
	if locale == "fr" {
		separator := ""
		for _, candidate := range []string{" ", "\u00a0", "\u202f"} {
			if strings.Contains(whole, candidate) {
				if separator != "" {
					return nil
				}
				separator = candidate
			}
		}
		group = separator
	}
	if group != "" && strings.Contains(whole, group) {
		chunks := strings.Split(whole, group)
		if len(chunks[0]) < 1 || len(chunks[0]) > 3 {
			return nil
		}
		for _, chunk := range chunks[1:] {
			if len(chunk) != 3 {
				return nil
			}
		}
		whole = strings.Join(chunks, "")
	}
	if len(whole) < 1 || len(whole) > 16 {
		return nil
	}
	for _, c := range whole {
		if c < '0' || c > '9' {
			return nil
		}
	}
	out := sign + whole
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) < 1 || len(fraction) > 8 {
			return nil
		}
		for _, c := range fraction {
			if c < '0' || c > '9' {
				return nil
			}
		}
		out += "." + fraction
	}
	return &out
}
func validateRawAmounts(w ReviewWrite) error {
	if w.InputLocale == "" && w.RawAmounts == nil {
		return nil
	}
	if w.RawAmounts == nil || w.InputLocale != "en" && w.InputLocale != "fr" && w.InputLocale != "he" || len(w.RawAmounts.TaxLines) != len(w.Metadata.TaxLines) {
		return ErrInvalid
	}
	for _, pair := range [][2]*string{{w.RawAmounts.Subtotal, w.Metadata.Subtotal}, {w.RawAmounts.Tax, w.Metadata.Tax}, {w.RawAmounts.Total, w.Metadata.Total}} {
		parsed := (*string)(nil)
		if pair[0] != nil {
			parsed = parseLocalizedReviewDecimal(*pair[0], w.InputLocale)
		}
		if (parsed == nil) != (pair[1] == nil) || parsed != nil && *parsed != *pair[1] {
			return ErrInvalid
		}
	}
	for i, raw := range w.RawAmounts.TaxLines {
		parsed := parseLocalizedReviewDecimal(raw, w.InputLocale)
		if parsed == nil || w.Metadata.TaxLines[i].Amount == nil || *parsed != *w.Metadata.TaxLines[i].Amount {
			return ErrInvalid
		}
	}
	return nil
}
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
	var cleanErr error
	if m.Title, cleanErr = cleanBounded(m.Title, 200, false); cleanErr != nil {
		return cleanErr
	}
	if m.Sender, cleanErr = cleanBounded(m.Sender, 300, false); cleanErr != nil {
		return cleanErr
	}
	if m.Reference, cleanErr = cleanBounded(m.Reference, 100, false); cleanErr != nil {
		return cleanErr
	}
	if m.Notes, cleanErr = cleanBounded(m.Notes, 2000, true); cleanErr != nil {
		return cleanErr
	}
	if m.DocumentType != "" && !IsInvoiceType(m.DocumentType) && !IsDocumentType(m.DocumentType) {
		return ErrInvalid
	}
	// Each type owns one field group. A conflicting non-empty value is rejected rather
	// than silently dropped, so a stale client cannot confirm mixed data.
	if IsDocumentType(m.DocumentType) && (m.Supplier != "" || m.InvoiceID != "" || m.AccountReference != "" || m.Currency != "" ||
		m.Subtotal != nil || m.Tax != nil || m.Total != nil || len(m.TaxLines) > 0) {
		return ErrInvalid
	}
	if IsInvoiceType(m.DocumentType) && (m.Title != "" || m.Sender != "" || m.Reference != "" || m.Notes != "") {
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
	if confirm && IsDocumentType(m.DocumentType) {
		if m.Title == "" {
			return ErrInvalid
		}
		return nil
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
	v := ReviewRead{DocumentID: documentID, History: []Revision{}, CandidateWarnings: []string{}, Candidates: ReviewMetadata{TaxLines: []TaxLine{}}}
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
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "correct"); e != nil {
		return ReviewRead{}, e
	}
	var digest string
	e = tx.QueryRowContext(ctx, `SELECT o.sha256 FROM invoyis.documents d JOIN invoyis.submissions sub ON sub.id=d.submission_id JOIN invoyis.stored_objects o ON o.id=sub.stored_object_id WHERE d.archive_id=$1 AND d.id=$2`, archiveID, documentID).Scan(&digest)
	if errors.Is(e, sql.ErrNoRows) {
		return ReviewRead{}, ErrNotFound
	}
	if e != nil {
		return ReviewRead{}, e
	}
	// A row lock serialises confirmation, clearance changes, and duplicate requests.
	var state, clearance string
	var version int64
	var prior sql.NullString
	e = tx.QueryRowContext(ctx, `SELECT state,clearance,review_version,COALESCE(latest_draft_revision_id,current_confirmed_revision_id) FROM invoyis.documents WHERE archive_id=$1 AND id=$2 FOR UPDATE`, archiveID, documentID).Scan(&state, &clearance, &version, &prior)
	if errors.Is(e, sql.ErrNoRows) {
		return ReviewRead{}, ErrNotFound
	}
	if e != nil {
		return ReviewRead{}, e
	}
	var lockedRole sql.NullString
	e = tx.QueryRowContext(ctx, `SELECT invoyis.lock_review_membership($1,$2)`, archiveID, actor).Scan(&lockedRole)
	if e != nil {
		return ReviewRead{}, e
	}
	if !lockedRole.Valid {
		return ReviewRead{}, ErrNotFound
	}
	if !Can(lockedRole.String, false, "correct") {
		return ReviewRead{}, ErrForbidden
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
	if w.Metadata.TaxLines == nil {
		w.Metadata.TaxLines = []TaxLine{}
	}
	if w.AcknowledgedWarnings == nil {
		w.AcknowledgedWarnings = []string{}
	}
	if e := validateMetadata(&w.Metadata, w.Action == "confirm"); e != nil {
		return ReviewRead{}, e
	}
	if e := validateRawAmounts(w); e != nil {
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
	fingerprint := reviewFingerprint(w)
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
	for _, key := range []string{"title", "sender", "reference", "notes"} {
		delete(sm, key) // extraction never proposes document-group fields
	}
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
// reviewFingerprint identifies a write for idempotent replay. Its JSON shape for existing
// payloads must not change, or a retry of a pre-existing write would be rejected as a conflict.
func reviewFingerprint(w ReviewWrite) string {
	body, _ := json.Marshal(w)
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
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
	if e := decodeJSONLimited(w, r, &input, 16*1024); e != nil {
		writeFailure(w, e, requestID)
		return
	}
	language, e := s.Language(r.Context(), actor)
	if e != nil {
		writeFailure(w, e, requestID)
		return
	}
	if input.RawAmounts == nil || input.InputLocale != language {
		writeFailure(w, ErrInvalid, requestID)
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
