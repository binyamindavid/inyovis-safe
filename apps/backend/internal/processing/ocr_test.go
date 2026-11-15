package processing

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

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
	if r.Candidates["invoiceId"] != "0000123" || r.Candidates["tax"] != "0.00" || r.Candidates["total"] != "119.00" || r.Candidates["currency"] != "EUR" {
		t.Fatalf("%v", r.Candidates)
	}
	if r.Provenance["invoiceId"].(map[string]any)["page"] != 2 {
		t.Fatal("missing page provenance")
	}
	readInvoiceLine(&r, "Total: 120.00 EUR", "digital", 1)
	if _, ok := r.Candidates["total"]; ok {
		t.Fatal("conflicting total must be blank")
	}
	readInvoiceLine(&r, "Currency ILS", "digital", 3)
	if _, ok := r.Candidates["currency"]; ok {
		t.Fatal("conflicting currency must be blank")
	}
}

func TestBoundedOutputKillsAndReapsChild(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pid")
	_, err := boundedOutput(context.Background(), 128, "sh", "-c", "echo $$ > \"$1\"; while :; do printf '0123456789'; done", "sh", file)
	if !errors.Is(err, errExtractionOutputLimit) {
		t.Fatalf("limit error = %v", err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw[:len(raw)-1]))
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child still exists: %v", err)
	}
}

func TestPDFPageLimitLeavesDocumentReviewable(t *testing.T) {
	if _, err := exec.LookPath("pdfunite"); err != nil {
		t.Skip("pdfunite unavailable")
	}
	dir := t.TempDir()
	one := filepath.Join(dir, "one.pdf")
	if err := os.WriteFile(one, syntheticPDF("Synthetic lease agreement with many pages and no invoice details"), 0600); err != nil {
		t.Fatal(err)
	}
	args := make([]string, 11)
	for i := range args {
		args[i] = one
	}
	all := filepath.Join(dir, "all.pdf")
	if err := exec.Command("pdfunite", append(args, all)...).Run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(all)
	if err != nil {
		t.Fatal(err)
	}
	result, err := parsePDF(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if !containsWarning(result.Warnings, "extraction_page_limit") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
}
