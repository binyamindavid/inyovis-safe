package archive

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestParseDocumentFiltersStatusSearchAndTypes(t *testing.T) {
	q := url.Values{"status": {"needs_review"}, "q": {"  Café 50% _x  "}, "types": {"invoice,credit_note"}}
	f, _, e := parseDocumentFilters(q, false)
	if e != nil || f.Status != "needs_review" || f.Query != "Café 50% _x" || f.Types != "invoice,credit_note" {
		t.Fatalf("parsed %+v %v", f, e)
	}
	if got := likePattern(f.Query); got != `%Café 50\% \_x%` {
		t.Fatalf("pattern %q", got)
	}
	if got := likePattern(`a\b`); got != `%a\\b%` {
		t.Fatalf("backslash pattern %q", got)
	}
	for name, v := range map[string]url.Values{
		"unknown status":  {"status": {"done"}},
		"types without q": {"types": {"invoice"}},
		"unknown type":    {"q": {"x"}, "types": {"invoice,receipt"}},
		"long query":      {"q": {strings.Repeat("א", maxBrowseQuery+1)}},
		"repeated q":      {"q": {"a", "b"}},
	} {
		if _, _, e := parseDocumentFilters(v, false); !errors.Is(e, ErrInvalid) {
			t.Fatalf("%s: %v", name, e)
		}
	}
	if f, _, e := parseDocumentFilters(url.Values{"q": {"x"}, "status": {"ready"}, "accountReference": {"A"}}, true); e != nil || f.AccountReference != "A" {
		t.Fatalf("supplier search: %+v %v", f, e)
	}
	if f, _, e := parseDocumentFilters(url.Values{"accountReference": {"00 1-A"}, "dateFrom": {"2026-01-01"}, "dateTo": {"2026-01-31"}, "total": {"0.00"}, "currency": {"EUR"}}, false); e != nil || f.AccountReference != "00 1-A" || f.Total != "0.00" {
		t.Fatalf("archive filters: %+v %v", f, e)
	}
	for name, v := range map[string]url.Values{
		"invalid date":            {"issueDate": {"2026-02-30"}},
		"year zero exact":         {"issueDate": {"0000-01-01"}},
		"year zero start":         {"dateFrom": {"0000-01-01"}},
		"year zero end":           {"dateTo": {"0000-12-31"}},
		"reversed dates":          {"dateFrom": {"2026-02-01"}, "dateTo": {"2026-01-31"}},
		"mixed date modes":        {"issueDate": {"2026-01-01"}, "dateFrom": {"2026-01-01"}},
		"amount without currency": {"total": {"0"}},
		"currency without amount": {"currency": {"EUR"}},
		"unsupported currency":    {"total": {"10.00"}, "currency": {"USD"}},
		"noncanonical amount":     {"total": {"01.00"}, "currency": {"EUR"}},
		"outer account space":     {"accountReference": {" 00 1-A"}},
	} {
		if _, _, e := parseDocumentFilters(v, false); !errors.Is(e, ErrInvalid) {
			t.Fatalf("%s: %v", name, e)
		}
	}
}

func TestBrowseCursorBindsSearchAndStatus(t *testing.T) {
	a := "22222222-2222-2222-2222-222222222222"
	f := DocumentFilters{Status: "attention", Query: "café"}
	raw := encodeBrowseCursor(browseCursor{Archive: a, Filters: f, ID: "44444444-4444-4444-4444-444444444444"})
	if _, e := decodeBrowseCursor(raw, a, "", f); e != nil {
		t.Fatal(e)
	}
	for _, other := range []DocumentFilters{{Status: "ready", Query: "café"}, {Status: "attention", Query: "cafe"}, {Status: "attention", Query: "café", Types: "invoice"}} {
		if _, e := decodeBrowseCursor(raw, a, "", other); !errors.Is(e, ErrInvalid) {
			t.Fatalf("cursor reused across %+v: %v", other, e)
		}
	}
	for _, other := range []DocumentFilters{{Status: "attention", Query: "café", IssueDate: "2026-01-01"}, {Status: "attention", Query: "café", Total: "0", Currency: "EUR"}, {Status: "attention", Query: "café", AccountReference: "00 1-A"}} {
		if _, e := decodeBrowseCursor(raw, a, "", other); !errors.Is(e, ErrInvalid) {
			t.Fatalf("cursor reused across %+v: %v", other, e)
		}
	}
	zero := "0000-01-01"
	if _, e := decodeBrowseCursor(encodeBrowseCursor(browseCursor{Archive: a, Filters: f, Date: &zero, ID: "44444444-4444-4444-4444-444444444444"}), a, "", f); !errors.Is(e, ErrInvalid) {
		t.Fatalf("year-zero cursor: %v", e)
	}
}
