package archive

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewDecoderAcceptsValidLargePayloadAndBoundsIt(t *testing.T) {
	amount := "1234567890123456.12345678"
	lines := make([]TaxLine, 32)
	rawLines := make([]string, 32)
	for i := range lines {
		lines[i] = TaxLine{Amount: &amount, Currency: "USD"}
		rawLines[i] = amount
	}
	input := ReviewWrite{
		Action: "draft", ExpectedVersion: 0,
		IdempotencyKey: "00000000-0000-4000-8000-000000000001",
		Metadata: ReviewMetadata{
			DocumentType: "invoice", Supplier: strings.Repeat("S", 400), InvoiceID: strings.Repeat("I", 400),
			AccountReference: strings.Repeat("A", 400), IssueDate: "2026-11-11", Currency: "USD",
			Subtotal: &amount, Total: &amount, TaxLines: lines,
		},
		InputLocale: "en", RawAmounts: &ReviewRawAmounts{Subtotal: &amount, Total: &amount, TaxLines: rawLines},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= 4096 || len(data) >= 16*1024 {
		t.Fatalf("unexpected fixture size %d", len(data))
	}
	request := httptest.NewRequest(http.MethodPut, "/review", bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	var decoded ReviewWrite
	if err := decodeJSONLimited(httptest.NewRecorder(), request, &decoded, 16*1024); err != nil {
		t.Fatalf("valid %d-byte review rejected: %v", len(data), err)
	}
	if err := validateMetadata(&decoded.Metadata, false); err != nil {
		t.Fatalf("metadata rejected: %v", err)
	}
	if err := validateRawAmounts(decoded); err != nil {
		t.Fatalf("raw amounts rejected: %v", err)
	}
	tooLarge := httptest.NewRequest(http.MethodPut, "/review", strings.NewReader(strings.Repeat("x", 16*1024+1)))
	tooLarge.Header.Set("Content-Type", "application/json")
	if err := decodeJSONLimited(httptest.NewRecorder(), tooLarge, &decoded, 16*1024); err == nil {
		t.Fatal("oversized review accepted")
	}
}
