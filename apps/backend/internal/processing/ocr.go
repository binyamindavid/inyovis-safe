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
	"time"
	"unicode"
)

const maxOCRPages = 10
const maxOCRTextBytes = 128 << 10

var errExtractionOutputLimit = errors.New("extraction output limit")

type extractionTools struct {
	info, text, raster, ocr string
	ocrTimeout, totalBudget time.Duration
}

var defaultExtractionTools = extractionTools{"pdfinfo", "pdftotext", "pdftoppm", "tesseract", 5 * time.Second, 18 * time.Second}

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

func extractPDFPages(ctx context.Context, pdf string, tools extractionTools) (Result, error) {
	result := Result{Candidates: map[string]any{}, Provenance: map[string]any{}, Warnings: []string{}}
	finish := func(warning string) (Result, error) {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if warning != "" {
			result.Warnings = append(result.Warnings, warning)
		}
		if len(result.Candidates) == 0 {
			result.Warnings = append(result.Warnings, "no_supported_xml")
		}
		return result, nil
	}
	budgetCtx, cancelBudget := context.WithTimeout(ctx, tools.totalBudget)
	defer cancelBudget()
	budgetExpired := func() bool { return ctx.Err() == nil && errors.Is(budgetCtx.Err(), context.DeadlineExceeded) }
	info, err := exec.CommandContext(budgetCtx, tools.info, pdf).Output()
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if budgetExpired() {
			return finish("extraction_time_limit")
		}
		return finish("extraction_info_unavailable")
	}
	match := pagesPattern.FindSubmatch(info)
	if len(match) != 2 {
		return finish("extraction_info_unavailable")
	}
	count, _ := strconv.Atoi(string(match[1]))
	if count < 1 {
		return finish("extraction_info_unavailable")
	}
	if count > maxOCRPages {
		result.Warnings = append(result.Warnings, "extraction_page_limit")
		count = maxOCRPages
	}
	for page := 1; page <= count; page++ {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if budgetExpired() {
			return finish("extraction_time_limit")
		}
		n := strconv.Itoa(page)
		text, err := boundedOutput(budgetCtx, maxOCRTextBytes, tools.text, "-f", n, "-l", n, "-layout", pdf, "-")
		if err != nil {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			if budgetExpired() {
				return finish("extraction_time_limit")
			}
			if errors.Is(err, errExtractionOutputLimit) {
				result.Warnings = append(result.Warnings, fmt.Sprintf("extraction_text_limit_page_%d", page))
				continue
			}
			result.Warnings = append(result.Warnings, fmt.Sprintf("extraction_text_failed_page_%d", page))
			text = nil
		}
		// The threshold controls whether to try OCR, not whether a short but
		// clearly labelled digital value may be suggested.
		digitalUsable := usableTextLayer(string(text))
		readInvoiceText(&result, text, "digital", page)
		if !digitalUsable {
			base := filepath.Join(filepath.Dir(pdf), "page-"+n)
			if err = exec.CommandContext(budgetCtx, tools.raster, "-f", n, "-l", n, "-singlefile", "-scale-to", "2200", "-png", pdf, base).Run(); err != nil {
				if ctx.Err() != nil {
					return Result{}, ctx.Err()
				}
				_ = os.Remove(base + ".png")
				if budgetExpired() {
					return finish("extraction_time_limit")
				}
				result.Warnings = append(result.Warnings, fmt.Sprintf("extraction_render_failed_page_%d", page))
				continue
			}
			image := base + ".png"
			stat, statErr := os.Stat(image)
			if statErr != nil || stat.Size() > 16<<20 {
				_ = os.Remove(image)
				result.Warnings = append(result.Warnings, fmt.Sprintf("extraction_render_limit_page_%d", page))
				continue
			}
			ocrCtx, cancel := context.WithTimeout(budgetCtx, tools.ocrTimeout)
			text, err = boundedOutput(ocrCtx, maxOCRTextBytes, tools.ocr, image, "stdout", "-l", "eng+fra+heb", "--psm", "3")
			ocrTimedOut := errors.Is(ocrCtx.Err(), context.DeadlineExceeded)
			cancel()
			_ = os.Remove(image)
			if err != nil {
				if ctx.Err() != nil {
					return Result{}, ctx.Err()
				}
				if budgetExpired() {
					return finish("extraction_time_limit")
				}
				if errors.Is(err, errExtractionOutputLimit) {
					result.Warnings = append(result.Warnings, fmt.Sprintf("extraction_text_limit_page_%d", page))
				} else if ocrTimedOut {
					result.Warnings = append(result.Warnings, fmt.Sprintf("ocr_timeout_page_%d", page))
				} else {
					result.Warnings = append(result.Warnings, fmt.Sprintf("ocr_engine_unavailable_page_%d", page))
				}
				continue
			}
			if !usableTextLayer(string(text)) {
				result.Warnings = append(result.Warnings, fmt.Sprintf("unreadable_page_%d", page))
			}
			readInvoiceText(&result, text, "ocr", page)
		}
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if budgetExpired() {
		return finish("extraction_time_limit")
	}
	return finish("")
}

func readInvoiceText(result *Result, text []byte, source string, page int) {
	for _, line := range bytes.Split(text, []byte{'\n'}) {
		if leaseHeading(strings.TrimSpace(stripBidiControls(string(line)))) && result.Candidates["documentType"] == nil {
			result.Candidates["documentType"] = "lease"
			result.Provenance["documentType"] = map[string]any{"source": source, "page": page}
			break
		}
	}
	// Keep raw text ephemeral. Only conservative, labelled values enter the run.
	for _, line := range bytes.Split(text, []byte{'\n'}) {
		full := strings.TrimSpace(string(line))
		readInvoiceLine(result, full, source, page)
		// -layout separates columns with wide gaps. Match a label only at the
		// start of its column, never in arbitrary prose elsewhere on the line.
		for _, column := range layoutColumns.Split(full, -1) {
			if column != full {
				readInvoiceLine(result, strings.TrimSpace(column), source, page)
			}
		}
	}
}

var leaseHeadingPattern = regexp.MustCompile(`^הסכם\s+שכירות\s+בלתי\s+מוגנת$`)

func leaseHeading(line string) bool {
	return leaseHeadingPattern.MatchString(line) || leaseHeadingPattern.MatchString(reverseRunes(line))
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

var layoutColumns = regexp.MustCompile(` {3,}|\t+`)
var invoiceNumberLine = regexp.MustCompile(`(?i)^(?:invoice\s*(?:number|no\.?|#)|facture\s*(?:n[°o]\.?|num[eé]ro))\s*[:#-]?\s*([^\s]+)\s*$`)
var amountLine = regexp.MustCompile(`(?i)^(subtotal|total\s*(?:ht|ttc)?|tax|tva|vat|sous.total|מע[״"]?מ|סך\s*הכל)\s*[:：-]?\s*([0-9]+(?:[.,][0-9]{2})?)\s*(EUR|ILS|€|₪)?\s*$`)
var printedID = regexp.MustCompile(`^[\pL\pN][\pL\pN._/-]{0,127}$`)
var hebrewMoney = regexp.MustCompile(`^(?:(₪|ILS)\s*)?([0-9]+(?:\.[0-9]{2})?)(?:\s*(₪|ILS))?$`)

func stripBidiControls(line string) string {
	return strings.Map(func(r rune) rune {
		if r == '\u200e' || r == '\u200f' || r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069' {
			return -1
		}
		return r
	}, line)
}

func reverseRunes(text string) string {
	runes := []rune(text)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func hebrewInvoiceID(line string) string {
	for _, label := range []string{"חשבונית מס/קבלה מספר", "מספר חשבונית"} {
		if strings.HasPrefix(line, label) {
			id := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(strings.TrimPrefix(line, label)), ":#-"))
			if printedID.MatchString(id) {
				return id
			}
		}
		reversed := reverseRunes(label)
		if strings.HasSuffix(line, reversed) {
			id := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(strings.TrimSuffix(line, reversed)), ":#-"))
			if printedID.MatchString(id) {
				return id
			}
		}
	}
	return ""
}

func hebrewAmount(line string) (field, amount, currency string) {
	// These full-document labels exclude table headings, line-item subtotals,
	// and other charges. Poppler may reverse their Hebrew glyph order.
	for _, item := range []struct{ label, field string }{
		{"סה\"כ לתשלום לפני מע\"מ", "subtotal"},
		{"סה\"כ בחשבונית זו לתשלום כולל מע\"מ", "total"},
	} {
		for _, label := range []string{item.label, reverseRunes(item.label)} {
			var value string
			switch {
			case strings.HasPrefix(line, label):
				value = strings.TrimSpace(strings.TrimPrefix(line, label))
			case strings.HasSuffix(line, label):
				value = strings.TrimSpace(strings.TrimSuffix(line, label))
			default:
				continue
			}
			m := hebrewMoney.FindStringSubmatch(value)
			if m == nil {
				continue
			}
			currency = m[1]
			if currency == "" {
				currency = m[3]
			}
			if currency == "₪" {
				currency = "ILS"
			}
			return item.field, m[2], currency
		}
	}
	return "", "", ""
}

func readInvoiceLine(result *Result, line, source string, page int) {
	line = strings.TrimSpace(stripBidiControls(line))
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
	if id := hebrewInvoiceID(line); id != "" {
		set("invoiceId", id)
		return
	}
	if field, amount, currency := hebrewAmount(strings.Join(strings.Fields(strings.ReplaceAll(line, "״", "\"")), " ")); field != "" {
		set(field, amount)
		if currency != "" {
			set("currency", currency)
		}
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
}
