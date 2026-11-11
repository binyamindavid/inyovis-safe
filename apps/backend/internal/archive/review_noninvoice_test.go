package archive

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func docMeta(kind, title string) ReviewMetadata {
	return ReviewMetadata{DocumentType: kind, Title: title, TaxLines: []TaxLine{}}
}

func TestNonInvoiceConfirmNeedsOnlyTitleAndType(t *testing.T) {
	for _, kind := range []string{TypeLease, TypeCourtCorrespondence, TypeOther} {
		m := docMeta(kind, "  Synthetic "+kind+"  ")
		if e := validateMetadata(&m, true); e != nil || m.Title != "Synthetic "+kind {
			t.Fatalf("%s: %+v %v", kind, m, e)
		}
		blank := docMeta(kind, " ")
		if e := validateMetadata(&blank, true); !errors.Is(e, ErrInvalid) {
			t.Fatalf("%s confirmed without title: %v", kind, e)
		}
		if e := validateMetadata(&blank, false); e != nil {
			t.Fatalf("%s draft needs no title: %v", kind, e)
		}
	}
}

func TestNonInvoiceRejectsConflictingInvoiceValues(t *testing.T) {
	zero := amount("0.00")
	for name, mutate := range map[string]func(*ReviewMetadata){
		"supplier":         func(m *ReviewMetadata) { m.Supplier = "Acme" },
		"invoiceId":        func(m *ReviewMetadata) { m.InvoiceID = "0042" },
		"accountReference": func(m *ReviewMetadata) { m.AccountReference = "A1" },
		"currency":         func(m *ReviewMetadata) { m.Currency = "ILS" },
		"subtotal":         func(m *ReviewMetadata) { m.Subtotal = zero },
		"explicit zero tax": func(m *ReviewMetadata) { m.Tax = zero },
		"total":            func(m *ReviewMetadata) { m.Total = amount("10.00") },
		"tax line":         func(m *ReviewMetadata) { m.TaxLines = []TaxLine{{Amount: zero, Currency: "ILS"}} },
	} {
		for _, confirm := range []bool{false, true} {
			m := docMeta(TypeLease, "Lease")
			mutate(&m)
			if e := validateMetadata(&m, confirm); !errors.Is(e, ErrInvalid) {
				t.Errorf("%s (confirm=%v) accepted: %v", name, confirm, e)
			}
		}
	}
}

func TestInvoiceRejectsDocumentGroupValues(t *testing.T) {
	for name, mutate := range map[string]func(*ReviewMetadata){
		"title": func(m *ReviewMetadata) { m.Title = "x" }, "sender": func(m *ReviewMetadata) { m.Sender = "x" },
		"reference": func(m *ReviewMetadata) { m.Reference = "x" }, "notes": func(m *ReviewMetadata) { m.Notes = "x" },
	} {
		m := ReviewMetadata{DocumentType: "invoice", Supplier: "A", InvoiceID: "1", IssueDate: "2026-11-11", Currency: "ILS", Total: amount("1.00"), TaxLines: []TaxLine{}}
		mutate(&m)
		if e := validateMetadata(&m, true); !errors.Is(e, ErrInvalid) {
			t.Errorf("invoice accepted %s: %v", name, e)
		}
	}
}

func TestInvoiceValidationUnchangedAndTaxNullVersusZero(t *testing.T) {
	m := ReviewMetadata{DocumentType: "credit_note", Supplier: "A", InvoiceID: "1", IssueDate: "2026-11-11", Currency: "EUR", Total: amount("-5.00"), TaxLines: []TaxLine{}}
	if e := validateMetadata(&m, true); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(m)
	if !strings.Contains(string(raw), `"tax":null`) || strings.Contains(string(raw), `"title"`) {
		t.Fatalf("invoice JSON %s", raw)
	}
	m.Tax = amount("0.00")
	raw, _ = json.Marshal(m)
	if !strings.Contains(string(raw), `"tax":"0.00"`) {
		t.Fatalf("explicit zero lost: %s", raw)
	}
	missing := ReviewMetadata{DocumentType: "invoice", TaxLines: []TaxLine{}}
	if e := validateMetadata(&missing, true); !errors.Is(e, ErrInvalid) {
		t.Fatal("invoice confirmed without invoice fields")
	}
}

func TestNonInvoiceJSONOmitsInvoiceFieldsAndKeepsText(t *testing.T) {
	m := docMeta(TypeCourtCorrespondence, "מכתב מבית המשפט / Court letter")
	m.Sender, m.Reference, m.Notes, m.IssueDate = "בית משפט השלום", "00417/2026", "שורה 1\nline 2", "2026-10-01"
	if e := validateMetadata(&m, true); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(m)
	for _, key := range []string{"supplier", "invoiceId", "accountReference", "currency", "subtotal", "tax", "total", "taxLines"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Fatalf("non-invoice JSON carries %s: %s", key, raw)
		}
	}
	var back ReviewMetadata
	if e := json.Unmarshal(raw, &back); e != nil || back.Reference != "00417/2026" || back.Sender != "בית משפט השלום" || back.Notes != "שורה 1\nline 2" {
		t.Fatalf("round trip %+v %v", back, e)
	}
}

func TestDocumentTextFieldsRejectUnsafeCharacters(t *testing.T) {
	for _, field := range []string{"title", "sender", "reference"} {
		m := docMeta(TypeOther, "T")
		bad := "A‮B"
		switch field {
		case "title":
			m.Title = bad
		case "sender":
			m.Sender = bad
		default:
			m.Reference = "A\nB"
		}
		if e := validateMetadata(&m, false); !errors.Is(e, ErrInvalid) {
			t.Errorf("%s accepted unsafe text", field)
		}
	}
	m := docMeta(TypeOther, "T")
	m.Notes = strings.Repeat("x", 2001)
	if e := validateMetadata(&m, false); !errors.Is(e, ErrInvalid) {
		t.Fatal("oversized notes accepted")
	}
	m.Notes = "A\x00B"
	if e := validateMetadata(&m, false); !errors.Is(e, ErrInvalid) {
		t.Fatal("control character in notes accepted")
	}
}

func TestUnknownTypeRejected(t *testing.T) {
	m := docMeta("contract", "x")
	if e := validateMetadata(&m, false); !errors.Is(e, ErrInvalid) {
		t.Fatal("unknown type accepted")
	}
}

func TestIndexRowBlankInvoiceColumnsAndExactValues(t *testing.T) {
	doc := docMeta(TypeLease, "Lease 12")
	doc.Sender, doc.Reference, doc.Notes, doc.IssueDate = "Dana ושות'", "000123", "Rothschild 1, תל אביב", "2026-01-01"
	row := IndexRow("11111111-1111-4111-8111-111111111111", doc)
	for i, col := range IndexColumns {
		blankExpected := col == "supplier" || col == "invoice_id" || col == "account_reference" || col == "currency" || col == "subtotal" || col == "tax" || col == "total"
		if blankExpected && row[i] != "" {
			t.Fatalf("invoice column %s not blank: %q", col, row[i])
		}
	}
	if row[5] != "000123" || row[2] != "Lease 12" {
		t.Fatalf("row %v", row)
	}
	inv := ReviewMetadata{DocumentType: "invoice", Supplier: "A", InvoiceID: "00042", Currency: "ILS", Tax: amount("0.00"), Total: amount("10.00"), TaxLines: []TaxLine{}}
	invRow := IndexRow("d", inv)
	if invRow[8] != "00042" || invRow[12] != "0.00" || invRow[11] != "" || invRow[2] != "" {
		t.Fatalf("invoice row %v", invRow)
	}
	var out strings.Builder
	w := csv.NewWriter(&out)
	_ = w.Write(IndexColumns)
	_ = w.Write(row)
	w.Flush()
	if !strings.Contains(out.String(), "000123") || !strings.Contains(out.String(), "תל אביב") {
		t.Fatalf("csv %q", out.String())
	}
	formula := docMeta(TypeOther, "=HYPERLINK(\"x\")")
	if got := IndexRow("d", formula)[2]; !strings.HasPrefix(got, "'=") {
		t.Fatalf("formula not neutralised: %q", got)
	}
}
