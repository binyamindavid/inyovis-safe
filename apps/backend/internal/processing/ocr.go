package processing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const maxOCRPages = 10
const maxOCRTextBytes = 128 << 10

var errExtractionOutputLimit = errors.New("extraction output limit")

var pagesPattern = regexp.MustCompile(`(?m)^Pages:\s+(\d+)\s*$`)

// A page's text layer is usable when it contains at least 24 letters or digits
// and at least two words. Short digital covers therefore do not mask scans.
func usableTextLayer(text string) bool {
	letters, words, inWord := 0, 0, false
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letters++
			if !inWord {
				words++
				inWord = true
			}
		} else {
			inWord = false
		}
	}
	return letters >= 24 && words >= 2
}

func extractPDFPages(ctx context.Context, pdf string) (Result, error) {
	result := Result{Candidates: map[string]any{}, Provenance: map[string]any{}, Warnings: []string{}}
	info, err := exec.CommandContext(ctx, "pdfinfo", pdf).Output()
	if err != nil {
		return Result{}, &ParseFailure{"invalid_pdf", false}
	}
	match := pagesPattern.FindSubmatch(info)
	if len(match) != 2 {
		return Result{}, &ParseFailure{"invalid_pdf", false}
	}
	count, _ := strconv.Atoi(string(match[1]))
	if count < 1 {
		return Result{}, &ParseFailure{"invalid_pdf", false}
	}
	if count > maxOCRPages {
		result.Warnings = append(result.Warnings, "extraction_page_limit")
		count = maxOCRPages
	}
	for page := 1; page <= count; page++ {
		n := strconv.Itoa(page)
		text, err := boundedOutput(ctx, maxOCRTextBytes, "pdftotext", "-f", n, "-l", n, "-layout", pdf, "-")
		if err != nil {
			if errors.Is(err, errExtractionOutputLimit) {
				return Result{}, &ParseFailure{"content_limit_exceeded", false}
			}
			return Result{}, &ParseFailure{"invalid_pdf", false}
		}
		source := "digital"
		if !usableTextLayer(string(text)) {
			source = "ocr"
			base := filepath.Join(filepath.Dir(pdf), "page-"+n)
			if err = exec.CommandContext(ctx, "pdftoppm", "-f", n, "-l", n, "-singlefile", "-scale-to", "2200", "-png", pdf, base).Run(); err != nil {
				return Result{}, &ParseFailure{"invalid_pdf", false}
			}
			image := base + ".png"
			stat, statErr := os.Stat(image)
			if statErr != nil || stat.Size() > 16<<20 {
				return Result{}, &ParseFailure{"content_limit_exceeded", false}
			}
			text, err = boundedOutput(ctx, maxOCRTextBytes, "tesseract", image, "stdout", "-l", "eng+fra+heb", "--psm", "3")
			_ = os.Remove(image)
			if err != nil {
				if errors.Is(err, errExtractionOutputLimit) {
					return Result{}, &ParseFailure{"content_limit_exceeded", false}
				}
				return Result{}, &ParseFailure{"ocr_engine_unavailable", false}
			}
		}
		if !usableTextLayer(string(text)) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("unreadable_page_%d", page))
			continue
		}
		// Keep raw text ephemeral. Only conservative, labelled values enter the run.
		for _, line := range bytes.Split(text, []byte{'\n'}) {
			readInvoiceLine(&result, strings.TrimSpace(string(line)), source, page)
		}
	}
	if len(result.Candidates) == 0 {
		result.Warnings = append(result.Warnings, "no_supported_xml")
	}
	return result, nil
}

// Read at most limit+1 bytes; a writer-side limit alone can leave a child
// blocked on a full pipe. Kill and reap it as soon as the limit is crossed.
func boundedOutput(ctx context.Context, limit int64, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	pipe, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = io.Discard
	if err = command.Start(); err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(pipe, limit+1))
	if readErr != nil || int64(len(data)) > limit {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if int64(len(data)) > limit {
		return nil, errExtractionOutputLimit
	}
	if waitErr != nil {
		return nil, waitErr
	}
	return data, nil
}

var invoiceNumberLine = regexp.MustCompile(`(?i)^(?:invoice\s*(?:number|no\.?|#)|facture\s*(?:n[°o]\.?|num[eé]ro)|מספר\s*חשבונית)\s*[:#-]?\s*([^\s]+)\s*$`)
var currencyLine = regexp.MustCompile(`(?i)\b(EUR|ILS)\b`)
var amountLine = regexp.MustCompile(`(?i)^(subtotal|total\s*(?:ht|ttc)?|tax|tva|vat|sous.total|מע[״"]?מ|סך\s*הכל)\s*[:：-]?\s*([0-9]+(?:[.,][0-9]{2})?)\s*(EUR|ILS|€|₪)?\s*$`)

func readInvoiceLine(result *Result, line, source string, page int) {
	if line == "" {
		return
	}
	set := func(field, value string) {
		for _, warning := range result.Warnings {
			if warning == "conflicting_"+field {
				return
			}
		}
		if old, ok := result.Candidates[field]; ok {
			if old != value {
				delete(result.Candidates, field)
				delete(result.Provenance, field)
				result.Warnings = append(result.Warnings, "conflicting_"+field)
			}
			return
		}
		result.Candidates[field] = value
		result.Provenance[field] = map[string]any{"source": source, "page": page}
	}
	if m := invoiceNumberLine.FindStringSubmatch(line); m != nil {
		set("invoiceId", m[1])
		return
	}
	if m := amountLine.FindStringSubmatch(line); m != nil {
		label := strings.ToLower(m[1])
		field := "total"
		if strings.Contains(label, "sub") || strings.Contains(label, "sous") || strings.Contains(label, "ht") {
			field = "subtotal"
		}
		if strings.Contains(label, "tax") || strings.Contains(label, "vat") || strings.Contains(label, "tva") || strings.Contains(label, "מע") {
			field = "tax"
		}
		set(field, strings.ReplaceAll(m[2], ",", "."))
		if m[3] != "" {
			currency := strings.ToUpper(m[3])
			if currency == "€" {
				currency = "EUR"
			}
			if currency == "₪" {
				currency = "ILS"
			}
			set("currency", currency)
		}
		return
	}
	if m := currencyLine.FindStringSubmatch(line); m != nil {
		set("currency", strings.ToUpper(m[1]))
	}
}
