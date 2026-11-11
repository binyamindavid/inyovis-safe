package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// The types below reproduce, byte for byte, the JSON shape the review write had before #35.
type legacyMetadata struct {
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
type legacyWrite struct {
	Action               string            `json:"action"`
	ExpectedVersion      int64             `json:"expectedVersion"`
	IdempotencyKey       string            `json:"idempotencyKey"`
	SourceRunID          *string           `json:"sourceRunId"`
	Metadata             legacyMetadata    `json:"metadata"`
	AcknowledgedWarnings []string          `json:"acknowledgedWarnings"`
	InputLocale          string            `json:"inputLocale,omitempty"`
	RawAmounts           *ReviewRawAmounts `json:"rawAmounts,omitempty"`
}

func legacyFingerprint(w ReviewWrite) string {
	m := w.Metadata
	body, _ := json.Marshal(legacyWrite{w.Action, w.ExpectedVersion, w.IdempotencyKey, w.SourceRunID,
		legacyMetadata{m.DocumentType, m.Supplier, m.InvoiceID, m.AccountReference, m.IssueDate, m.Currency, m.Subtotal, m.Tax, m.Total, m.TaxLines},
		w.AcknowledgedWarnings, w.InputLocale, w.RawAmounts})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func TestFingerprintMatchesPreChangeShapeForExistingPayloads(t *testing.T) {
	key := "6f1d0b0e-0f0a-4c55-8a58-1b2f3c4d5e6f"
	for name, meta := range map[string]ReviewMetadata{
		"undecided empty draft":   {TaxLines: []TaxLine{}},
		"undecided partial draft": {Supplier: "Acme", InvoiceID: "0042", Total: amount("1.00"), TaxLines: []TaxLine{}},
		"invoice":                 {DocumentType: "invoice", Supplier: "A", InvoiceID: "00042", IssueDate: "2026-11-11", Currency: "ILS", Total: amount("10.00"), Tax: amount("0.00"), TaxLines: []TaxLine{{Amount: amount("0.00"), Currency: "ILS", Provenance: ""}}},
		"credit note":             {DocumentType: "credit_note", Supplier: "ספק", InvoiceID: "000-1", IssueDate: "2026-11-11", Currency: "EUR", Total: amount("-5.00"), TaxLines: []TaxLine{}},
	} {
		w := ReviewWrite{Action: "draft", ExpectedVersion: 3, IdempotencyKey: key, Metadata: meta, AcknowledgedWarnings: []string{}, InputLocale: "he", RawAmounts: &ReviewRawAmounts{Total: meta.Total, TaxLines: []string{}}}
		if got, want := reviewFingerprint(w), legacyFingerprint(w); got != want {
			t.Errorf("%s: fingerprint %s differs from pre-change %s", name, got, want)
		}
	}
}

func TestReplayOfReceiptCreatedByOldCodeReturnsReceipt(t *testing.T) {
	s, _, owner, actor := setup(t)
	ctx := context.Background()
	store := &fakeOriginals{objects: map[string][]byte{}}
	s.Storage = store
	archive, _ := s.Create(ctx, actor, "personal", "Replay", "")
	docID, _, _ := clearedUpload(t, s, owner, store, actor, archive.ID, "replay")
	// An undecided draft, as the review page saves before the reviewer picks a type.
	key, _ := newID()
	w := ReviewWrite{Action: "draft", ExpectedVersion: 0, IdempotencyKey: key, Metadata: ReviewMetadata{Supplier: "Acme", InvoiceID: "00042", TaxLines: []TaxLine{}}, AcknowledgedWarnings: []string{}}
	first, err := s.WriteReview(ctx, actor, archive.ID, docID, w)
	if err != nil {
		t.Fatal(err)
	}
	// Make the stored receipt exactly what the old code would have recorded.
	prepared := w
	if _, err = owner.Exec(`UPDATE invoyis.review_idempotency SET fingerprint=$1 WHERE document_id=$2 AND idempotency_key=$3`, legacyFingerprint(prepared), docID, key); err != nil {
		t.Fatal(err)
	}
	replay, err := s.WriteReview(ctx, actor, archive.ID, docID, w)
	if err != nil || replay.ReviewVersion != first.ReviewVersion || len(replay.History) != 1 {
		t.Fatalf("replay of old receipt: %+v %v", replay, err)
	}
	var revisions int
	if err = owner.QueryRow(`SELECT count(*) FROM invoyis.review_idempotency WHERE document_id=$1`, docID).Scan(&revisions); err != nil || revisions != 1 {
		t.Fatalf("receipts=%d %v", revisions, err)
	}
}
