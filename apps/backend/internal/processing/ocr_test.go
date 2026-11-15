package processing

import "testing"

func TestUsableTextLayer(t *testing.T) {
	if usableTextLayer("Cover") {
		t.Fatal("short cover is not an invoice text layer")
	}
	if !usableTextLayer("Facture numéro 0000123 Fournisseur Société Exemple") {
		t.Fatal("French digital text should be usable")
	}
	if !usableTextLayer("חשבונית מספר 0000123 ספק חברה לדוגמה") {
		t.Fatal("Hebrew digital text should be usable")
	}
}

func TestInvoiceLineCandidates(t *testing.T) {
	r := Result{Candidates: map[string]any{}, Provenance: map[string]any{}}
	readInvoiceLine(&r, "Invoice No: 0000123", "ocr", 2)
	readInvoiceLine(&r, "VAT: 0.00", "ocr", 2)
	readInvoiceLine(&r, "Total: 119.00 EUR", "digital", 1)
	if r.Candidates["invoiceId"] != "0000123" || r.Candidates["tax"] != "0.00" || r.Candidates["total"] != "119.00" {
		t.Fatalf("%v", r.Candidates)
	}
	if r.Provenance["invoiceId"].(map[string]any)["page"] != 2 {
		t.Fatal("missing page provenance")
	}
	readInvoiceLine(&r, "Total: 120.00 EUR", "digital", 1)
	if _, ok := r.Candidates["total"]; ok {
		t.Fatal("conflicting total must be blank")
	}
}
