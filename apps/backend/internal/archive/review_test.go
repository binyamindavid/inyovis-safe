package archive

import (
	"testing"
)

func TestReviewValidationExactAmountsAndText(t *testing.T) {
	base := func() ReviewMetadata {
		return ReviewMetadata{DocumentType: "credit_note", Supplier: "  ספק ABC  ", InvoiceID: "000-42", IssueDate: "2026-11-11", Currency: "ILS", Total: amount("-0.00000001"), TaxLines: []TaxLine{}}
	}
	good := base()
	if e := validateMetadata(&good, true); e != nil || good.Supplier != "ספק ABC" || good.InvoiceID != "000-42" {
		t.Fatalf("valid: %+v %v", good, e)
	}
	for _, value := range []string{"1e2", "1,000", "12345678901234567", "1.123456789", ".5", "1.", "--1"} {
		m := base()
		m.Total = amount(value)
		if validateMetadata(&m, true) == nil {
			t.Errorf("accepted %q", value)
		}
	}
	for _, currency := range []string{"XYZ", "ils", "ZZZ"} {
		m := base()
		m.Currency = currency
		if validateMetadata(&m, true) == nil {
			t.Errorf("accepted %s", currency)
		}
	}
	for _, supplier := range []string{"A\x00B", "A\u202eB"} {
		m := base()
		m.Supplier = supplier
		if validateMetadata(&m, true) == nil {
			t.Errorf("accepted unsafe supplier")
		}
	}
	for _, date := range []string{"2026-02-30", "2026-11-11T00:00:00Z", "11/11/2026"} {
		m := base()
		m.IssueDate = date
		if validateMetadata(&m, true) == nil {
			t.Errorf("accepted %s", date)
		}
	}
	incomplete := ReviewMetadata{TaxLines: []TaxLine{}}
	if e := validateMetadata(&incomplete, false); e != nil {
		t.Fatal(e)
	}
	if e := validateMetadata(&incomplete, true); e == nil {
		t.Fatal("confirmed incomplete review")
	}
}
func TestReviewWarningsAndExtractionSeed(t *testing.T) {
	m := ReviewMetadata{Currency: "ILS", Subtotal: amount("0.1"), Tax: amount("0.2"), Total: amount("0.3"), TaxLines: []TaxLine{{Amount: amount("0.1"), Currency: "ILS"}, {Amount: amount("0.1"), Currency: "ILS"}}}
	if got := warningsFor(m); len(got) != 0 {
		t.Fatalf("exact sum %v", got)
	}
	m.Total = amount("0.31")
	m.TaxLines[1].Currency = "EUR"
	if got := warningsFor(m); len(got) != 2 || got[0] != "TOTAL_MISMATCH" || got[1] != "TAX_CURRENCY_MISMATCH" {
		t.Fatalf("warnings %v", got)
	}
	seed := extractionMetadata([]byte(`{"supplier":"ספק ABC","invoiceId":"00042","issueDate":"20261111","taxLines":["1.20","0.80"]}`))
	if seed.IssueDate != "2026-11-11" || len(seed.TaxLines) != 2 || *seed.TaxLines[0].Amount != "1.20" {
		t.Fatalf("seed %+v", seed)
	}
}
