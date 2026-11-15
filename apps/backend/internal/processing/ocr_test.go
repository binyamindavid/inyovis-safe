package processing

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

func TestLeaseHeadingSuggestion(t *testing.T) {
	r := Result{Candidates: map[string]any{}, Provenance: map[string]any{}}
	readInvoiceText(&r, []byte("\u202bהסכם שכירות בלתי מוגנת\u202c\n"), "ocr", 3)
	if r.Candidates["documentType"] != "lease" {
		t.Fatalf("lease suggestion = %v", r.Candidates)
	}
	if got := r.Provenance["documentType"].(map[string]any); got["source"] != "ocr" || got["page"] != 3 {
		t.Fatalf("lease provenance = %v", got)
	}
	for _, line := range []string{"הסכם שכירות", "הדיון בהסכם שכירות בלתי מוגנת נדחה"} {
		other := Result{Candidates: map[string]any{}, Provenance: map[string]any{}}
		readInvoiceText(&other, []byte(line), "digital", 1)
		if other.Candidates["documentType"] != nil {
			t.Fatalf("false lease suggestion: %q", line)
		}
	}
}

func TestLeaseOCRUsesCanonicalImagePath(t *testing.T) {
	for _, tool := range []string{"pdfdetach", "pdfinfo", "pdftotext", "pdftoppm", "realpath"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	ocr := filepath.Join(t.TempDir(), "ocr")
	if err := os.WriteFile(ocr, []byte("#!/bin/sh\n[ \"$1\" = \"$(realpath \"$1\")\" ] || exit 2\nprintf 'הסכם שכירות בלתי מוגנת\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tools := defaultExtractionTools
	tools.ocr = ocr
	result, err := parsePDFWithTools(context.Background(), syntheticPDF(""), tools)
	if err != nil {
		t.Fatal(err)
	}
	if result.Candidates["documentType"] != "lease" {
		t.Fatalf("candidates=%v warnings=%v", result.Candidates, result.Warnings)
	}
	if got := result.Provenance["documentType"].(map[string]any); got["source"] != "ocr" || got["page"] != 1 {
		t.Fatalf("provenance=%v", got)
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

func TestSyntheticDigitalInvoiceLayouts(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("Poppler required")
	}
	for _, tc := range []struct {
		name, file, id string
		amounts        map[string]string
	}{
		{"English columns", "digital_columns_en.pdf", "INV00001234", nil},
		{"Hebrew bidi", "digital_bidi_he.pdf", "00001234", map[string]string{"subtotal": "100.00", "total": "117.00", "currency": "ILS"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join("testdata", tc.file)
			layout, err := exec.Command("pdftotext", "-layout", path, "-").Output()
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "English columns" && !strings.Contains(string(layout), "       Invoice Number: INV00001234") {
				t.Fatalf("column boundary missing: %q", layout)
			}
			if tc.name == "Hebrew bidi" {
				if !strings.ContainsRune(string(layout), '\u202b') || !strings.Contains(string(layout), "00001234") || !strings.Contains(string(layout), "₪ 100.00") {
					t.Fatalf("bidi or amount-before-label evidence missing: %q", layout)
				}
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			result, err := parsePDF(context.Background(), data)
			if err != nil {
				t.Fatal(err)
			}
			if result.Candidates["invoiceId"] != tc.id {
				t.Fatalf("invoice ID = %v, warnings = %v", result.Candidates["invoiceId"], result.Warnings)
			}
			for field, want := range tc.amounts {
				if result.Candidates[field] != want {
					t.Errorf("%s = %v, want %s", field, result.Candidates[field], want)
				}
			}
			if tc.name == "English columns" {
				for _, field := range []string{"currency", "subtotal", "tax", "total"} {
					if _, exists := result.Candidates[field]; exists {
						t.Errorf("USD fixture unexpectedly suggested %s = %v", field, result.Candidates[field])
					}
				}
			}
			for _, field := range append([]string{"invoiceId"}, keysOf(tc.amounts)...) {
				prov, ok := result.Provenance[field].(map[string]any)
				if !ok || prov["source"] != "digital" || prov["page"] != 1 {
					t.Errorf("%s provenance = %v", field, result.Provenance[field])
				}
			}
		})
	}
}

func keysOf(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestHebrewLabelsRejectUnrelatedAmounts(t *testing.T) {
	r := Result{Candidates: map[string]any{}, Provenance: map[string]any{}}
	readInvoiceLine(&r, "מספר חשבונית \u200e00004567\u200f", "digital", 1)
	readInvoiceLine(&r, "₪ 50.00   סה\"כ שורת פריט", "digital", 1)
	readInvoiceLine(&r, "₪ 70.00   חיוב נוסף", "digital", 1)
	readInvoiceLine(&r, "כותרת טבלה: סה\"כ", "digital", 1)
	if r.Candidates["invoiceId"] != "00004567" {
		t.Fatalf("invoice ID = %v", r.Candidates["invoiceId"])
	}
	for _, field := range []string{"subtotal", "total", "currency"} {
		if _, ok := r.Candidates[field]; ok {
			t.Fatalf("unrelated line suggested %s", field)
		}
	}
}

func TestInvoiceNumberInProseIsNotAColumn(t *testing.T) {
	r := Result{Candidates: map[string]any{}, Provenance: map[string]any{}}
	readInvoiceText(&r, []byte("Please quote Invoice Number: REF123 when paying"), "digital", 1)
	if _, ok := r.Candidates["invoiceId"]; ok {
		t.Fatal("prose became printed invoice number")
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

func TestHebrewInvoiceSupplierFromRegistrationHeader(t *testing.T) {
	result := Result{Candidates: map[string]any{}, Provenance: map[string]any{}}
	text := "חשבונית מס/קבלה מספר 00001234\n" +
		"חברה לדוגמה בע\"מ   תאריך החשבונית\n" +
		"ח.פ123456789 .\n" +
		"לקוח לדוגמה בע\"מ\n"
	readInvoiceText(&result, []byte(text), "digital", 1)
	if result.Candidates["supplier"] != "חברה לדוגמה בע\"מ" {
		t.Fatalf("supplier = %v", result.Candidates["supplier"])
	}
	evidence, ok := result.Provenance["supplier"].(map[string]any)
	if !ok || evidence["source"] != "digital" || evidence["page"] != 1 {
		t.Fatalf("supplier provenance = %v", evidence)
	}
}

func TestDigitalStatementIssuer(t *testing.T) {
	text := "Example Studio                Invoice statement\n" +
		"(SampleBrand)                Invoice number: INV-0000114\n" +
		"Bill to                      Date\n" +
		"Example Customer\n"
	result := Result{Candidates: map[string]any{}, Provenance: map[string]any{}}
	readInvoiceText(&result, []byte(text), "digital", 1)
	if result.Candidates["supplier"] != "Example Studio (SampleBrand)" {
		t.Fatalf("unexpected supplier: %v", result.Candidates["supplier"])
	}
	result = Result{Candidates: map[string]any{}, Provenance: map[string]any{}}
	readInvoiceText(&result, []byte("Invoice number: INV-0000114\nBill to\nExample Customer\n"), "digital", 1)
	if result.Candidates["supplier"] != nil {
		t.Fatal("customer became the supplier")
	}
}

func TestGovernmentCertificateSuggestion(t *testing.T) {
	result := Result{Candidates: map[string]any{}, Provenance: map[string]any{}}
	readInvoiceText(&result, []byte("המוסד לביטוח לאומי\nאישור זכאות לגמלאות לתקופה מ 01/2025\n"), "digital", 1)
	if result.Candidates["documentType"] != "government_certificate" || result.Candidates["sender"] != "המוסד לביטוח לאומי" || result.Candidates["title"] != "אישור זכאות לגמלאות" {
		t.Fatalf("unexpected government certificate candidates: %v", result.Candidates)
	}
	result = Result{Candidates: map[string]any{}, Provenance: map[string]any{}}
	readInvoiceText(&result, []byte("המוסד לביטוח לאומי\nInvoice number: INV-123\n"), "digital", 1)
	if result.Candidates["documentType"] != nil || result.Candidates["sender"] != nil {
		t.Fatal("agency name alone must not suggest a certificate")
	}
}
