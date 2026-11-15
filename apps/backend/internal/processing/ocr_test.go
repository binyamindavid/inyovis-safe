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
	"time"
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
	readInvoiceLine(&r, "tant qu’ils ne sont pas payés", "digital", 3)
	if r.Candidates["currency"] != "EUR" {
		t.Fatal("French prose changed currency")
	}
	readInvoiceLine(&r, "Total: 119.00 ILS", "digital", 3)
	if _, ok := r.Candidates["currency"]; ok {
		t.Fatal("conflicting currency must be blank")
	}
}

func TestCurrencyOnlyFromAmountLines(t *testing.T) {
	r := Result{Candidates: map[string]any{}, Provenance: map[string]any{}}
	readInvoiceLine(&r, "tant qu’ils ne sont pas payés", "digital", 1)
	readInvoiceLine(&r, "The invoice may be paid in EUR later", "digital", 1)
	if _, ok := r.Candidates["currency"]; ok {
		t.Fatal("prose supplied currency")
	}
	readInvoiceLine(&r, "Total: 119.00 EUR", "digital", 1)
	if r.Candidates["currency"] != "EUR" {
		t.Fatalf("currency = %v", r.Candidates["currency"])
	}
	readInvoiceLine(&r, "Tax: 0.00 €", "digital", 1)
	if r.Candidates["currency"] != "EUR" {
		t.Fatal("euro symbol disagreed with EUR")
	}
	readInvoiceLine(&r, "Total: 119.00 ILS", "digital", 2)
	if _, ok := r.Candidates["currency"]; ok {
		t.Fatal("genuine amount-line conflict must clear currency")
	}
}

func TestShortDigitalCandidateSurvivesOCRFailure(t *testing.T) {
	data := syntheticPDF("Invoice No: 0000123")
	for _, tc := range []struct {
		name, script, warning string
		raster                bool
		timeout               time.Duration
	}{
		{"render failure", "exit 1", "extraction_render_failed_page_1", true, 0},
		{"OCR unavailable", "exit 1", "ocr_engine_unavailable_page_1", false, 0},
		{"OCR timeout", "while :; do :; done", "ocr_timeout_page_1", false, 500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "attempted")
			script := filepath.Join(t.TempDir(), "tool")
			body := "#!/bin/sh\nprintf attempted > '" + marker + "'\n" + tc.script + "\n"
			if err := os.WriteFile(script, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			tools := defaultExtractionTools
			if tc.raster {
				tools.raster = script
			} else {
				tools.ocr = script
			}
			if tc.timeout > 0 {
				tools.ocrTimeout = tc.timeout
			}
			result, err := parsePDFWithTools(context.Background(), data, tools)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("OCR path was not attempted: %v", err)
			}
			if result.Candidates["invoiceId"] != "0000123" || !containsWarning(result.Warnings, tc.warning) {
				t.Fatalf("result = %+v", result)
			}
			prov := result.Provenance["invoiceId"].(map[string]any)
			if prov["source"] != "digital" || prov["page"] != 1 {
				t.Fatalf("provenance = %v", prov)
			}
		})
	}
}

func TestPDFInfoFailureIsReviewWarning(t *testing.T) {
	tools := defaultExtractionTools
	tools.info = filepath.Join(t.TempDir(), "missing-pdfinfo")
	result, err := parsePDFWithTools(context.Background(), syntheticPDF("Valid document"), tools)
	if err != nil || !containsWarning(result.Warnings, "extraction_info_unavailable") {
		t.Fatalf("result=%+v err=%v", result, err)
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
