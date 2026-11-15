package processing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// syntheticPDF builds a small valid PDF with one page of text and no attachments, the
// shape of a scanned lease or court letter: nothing for the invoice parser to extract.
func syntheticPDF(text string) []byte {
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		"",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	stream := fmt.Sprintf("BT /F1 12 Tf 20 100 Td (%s) Tj ET", text)
	objects[3] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, o := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return []byte(b.String())
}

func TestClearedPDFWithoutInvoiceCandidatesReachesNeedsReview(t *testing.T) {
	ownerURL, workerURL := os.Getenv("PROCESSING_TEST_DATABASE_URL"), os.Getenv("DOCUMENT_WORKER_TEST_DATABASE_URL")
	if ownerURL == "" || workerURL == "" {
		t.Skip("disposable PostgreSQL URLs required")
	}
	if _, err := exec.LookPath("pdfdetach"); err != nil {
		t.Skip("pdfdetach required for the real PDF parser")
	}
	owner, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	clean := func(context.Context, []byte) (bool, error) { return true, nil }
	createTool := func(name, body string) string {
		path := t.TempDir() + "/" + name
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	missingOCR := defaultExtractionTools
	missingOCR.ocr = t.TempDir() + "/missing-tesseract"
	timedOutOCR := defaultExtractionTools
	timedOutOCR.ocr = createTool("slow-ocr", "while :; do :; done")
	timedOutOCR.ocrTimeout = 20 * time.Millisecond
	textLimit := defaultExtractionTools
	textLimit.text = createTool("long-text", "while :; do printf '0123456789'; done")
	cases := []struct {
		name             string
		data             []byte
		scan             Scanner
		wantDoc          string
		wantClearance    string
		wantJob          string
		wantRuns         int
		wantEmptyOutcome bool
		tools            *extractionTools
		wantWarning      string
	}{
		{name: "lease", data: syntheticPDF("Synthetic lease agreement"), scan: clean, wantDoc: "needs_review", wantClearance: "cleared", wantJob: "succeeded", wantRuns: 1, wantEmptyOutcome: true},
		{name: "court letter", data: syntheticPDF("Synthetic court letter"), scan: clean, wantDoc: "needs_review", wantClearance: "cleared", wantJob: "succeeded", wantRuns: 1, wantEmptyOutcome: true},
		{name: "other", data: syntheticPDF("Synthetic warranty card"), scan: clean, wantDoc: "needs_review", wantClearance: "cleared", wantJob: "succeeded", wantRuns: 1, wantEmptyOutcome: true},
		{name: "missing OCR engine", data: syntheticPDF("Synthetic lease agreement"), scan: clean, wantDoc: "needs_review", wantClearance: "cleared", wantJob: "succeeded", wantRuns: 1, wantEmptyOutcome: true, tools: &missingOCR, wantWarning: "ocr_engine_unavailable_page_1"},
		{name: "OCR timeout", data: syntheticPDF("Synthetic lease agreement"), scan: clean, wantDoc: "needs_review", wantClearance: "cleared", wantJob: "succeeded", wantRuns: 1, wantEmptyOutcome: true, tools: &timedOutOCR, wantWarning: "ocr_timeout_page_1"},
		{name: "text output limit", data: syntheticPDF("Synthetic lease agreement"), scan: clean, wantDoc: "needs_review", wantClearance: "cleared", wantJob: "succeeded", wantRuns: 1, wantEmptyOutcome: true, tools: &textLimit, wantWarning: "extraction_text_limit_page_1"},
		{name: "blocked by scanner", data: syntheticPDF("Synthetic blocked"), scan: func(context.Context, []byte) (bool, error) { return false, nil }, wantClearance: "blocked", wantJob: "failed"},
		{name: "invalid pdf is a genuine failure", data: []byte("%PDF-1.7\nnot really a pdf\n%%EOF"), scan: clean, wantDoc: "failed", wantJob: "failed"},
		{name: "scanner outage stays pending", data: syntheticPDF("Synthetic outage"), scan: func(context.Context, []byte) (bool, error) { return false, errors.New("scanner down") }, wantDoc: "pending"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key := seedJob(t, owner, c.data)
			storage := &memoryStorage{objects: map[string][]byte{key: c.data}}
			parser := Parser(parsePDF)
			if c.tools != nil {
				parser = func(ctx context.Context, data []byte) (Result, error) { return parsePDFWithTools(ctx, data, *c.tools) }
			}
			engine, err := Open(workerURL, storage, c.scan, parser, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			claim, err := engine.Claim(context.Background())
			if err != nil || claim == nil {
				t.Fatalf("claim %v %v", claim, err)
			}
			if err = engine.Process(context.Background(), *claim); err != nil {
				t.Fatalf("process: %v", err)
			}
			var job, doc, clearance string
			var runs int
			var warnings sql.NullString
			err = owner.QueryRow(`SELECT j.state,d.state,d.clearance,(SELECT count(*) FROM invoyis.extraction_runs WHERE document_id=d.id),(SELECT warnings::text FROM invoyis.extraction_runs WHERE document_id=d.id LIMIT 1) FROM invoyis.processing_jobs j JOIN invoyis.documents d ON d.id=j.document_id WHERE j.id=$1`, claim.JobID).Scan(&job, &doc, &clearance, &runs, &warnings)
			if err != nil {
				t.Fatal(err)
			}
			if c.wantDoc != "" && doc != c.wantDoc || doc == "needs_review" && c.wantDoc != "needs_review" {
				t.Fatalf("document=%s want %q (job=%s clearance=%s)", doc, c.wantDoc, job, clearance)
			}
			if c.wantClearance != "" && clearance != c.wantClearance {
				t.Fatalf("clearance=%s want %s", clearance, c.wantClearance)
			}
			if c.wantJob != "" && job != c.wantJob || runs != c.wantRuns {
				t.Fatalf("job=%s runs=%d want job %q runs %d", job, runs, c.wantJob, c.wantRuns)
			}
			if c.wantEmptyOutcome && (!warnings.Valid || !strings.Contains(warnings.String, "no_supported_xml")) {
				t.Fatalf("warnings=%v", warnings)
			}
			if c.wantWarning != "" && (!warnings.Valid || !strings.Contains(warnings.String, c.wantWarning)) {
				t.Fatalf("warnings=%v, want %s", warnings, c.wantWarning)
			}
		})
	}
}
