package retention

import (
	"reflect"
	"testing"
	"time"
)

func synthetic(id, scope, trigger string, years int) Rule {
	return Rule{ID: id, Version: 1, Jurisdiction: "FR", EntityScope: "company", DocumentScope: scope, EffectiveFrom: "2020-01-01", EffectiveUntil: "2030-01-01", SelectionDate: "issue_date", Source: "synthetic", Trigger: trigger, Years: years, Active: true}
}
func syntheticCoverage(id, scope, status string, from, until Date) Coverage {
	return Coverage{ID: id, Jurisdiction: "FR", EntityScope: "company", DocumentScope: scope, EffectiveFrom: from, EffectiveUntil: until, SelectionDate: "issue_date", Status: status, Source: "synthetic"}
}
func TestDeterministicOverlapAndMissingFiling(t *testing.T) {
	a := synthetic("a", "invoice", "financial_year_end", 3)
	b := synthetic("b", "invoice", "filing_date", 5)
	f := Facts{Kind: "business", Jurisdiction: "FR", Entity: "company", DocumentType: "invoice", IssueDate: "2024-02-29", TaxYear: 2024, FinancialYearEnd: "2024-02-29"}
	first, e := EvaluateWithCoverage(f, []Rule{b, a}, []Coverage{syntheticCoverage("invoice", "invoice", "complete", "2020-01-01", "2030-01-01")})
	if e != nil || first.Status != "pending" || first.KeepThrough != "" || first.LowerBound != "2027-02-28" {
		t.Fatalf("%+v %v", first, e)
	}
	f.FilingDate = "2025-01-31"
	want, e := EvaluateWithCoverage(f, []Rule{b, a}, []Coverage{syntheticCoverage("invoice", "invoice", "complete", "2020-01-01", "2030-01-01")})
	if e != nil || want.Status != "calculated" || want.KeepThrough != "2030-01-31" {
		t.Fatalf("%+v %v", want, e)
	}
	reversed, _ := EvaluateWithCoverage(f, []Rule{a, b}, []Coverage{syntheticCoverage("invoice", "invoice", "complete", "2020-01-01", "2030-01-01")})
	if !reflect.DeepEqual(want, reversed) {
		t.Fatal("rule order changed result")
	}
	old, _ := EvaluateWithCoverage(Facts{Kind: "business", Jurisdiction: "FR", Entity: "company", DocumentType: "invoice", IssueDate: "2024-02-29", TaxYear: 2024, FinancialYearEnd: "2024-02-29"}, []Rule{a, b}, []Coverage{syntheticCoverage("invoice", "invoice", "complete", "2020-01-01", "2030-01-01")})
	if !reflect.DeepEqual(first, old) {
		t.Fatal("historical inputs changed")
	}
}
func TestScopeCoverageAndWindow(t *testing.T) {
	r := synthetic("invoice", "invoice", "issue_date", 1)
	coverage := []Coverage{syntheticCoverage("invoice", "invoice", "complete", "2020-01-01", "2030-01-01")}
	f := Facts{Kind: "business", Jurisdiction: "FR", Entity: "company", DocumentType: "lease", IssueDate: "2024-01-01"}
	for _, typ := range []string{"lease", "court_correspondence", "other"} {
		f.DocumentType = typ
		o, _ := EvaluateWithCoverage(f, []Rule{r}, coverage)
		if o.Status != "pending" || o.KeepThrough != "" || o.LowerBound != "" || !reflect.DeepEqual(o.Reasons, []string{"coverage_missing"}) {
			t.Fatalf("%s: %+v", typ, o)
		}
	}
	f.DocumentType = ""
	o, _ := EvaluateWithCoverage(f, []Rule{r}, coverage)
	if o.Status != "pending" {
		t.Fatal(o)
	}
	f.DocumentType = "invoice"
	f.Entity = ""
	o, _ = EvaluateWithCoverage(f, []Rule{r}, coverage)
	if o.Status != "pending" {
		t.Fatal(o)
	}
	f.Entity = "company"
	f.Jurisdiction = "IL"
	o, _ = EvaluateWithCoverage(f, []Rule{r}, coverage)
	if o.Status != "pending" {
		t.Fatal(o)
	}
	f.Jurisdiction = "FR"
	f.IssueDate = "2020-01-01"
	o, _ = EvaluateWithCoverage(f, []Rule{r}, coverage)
	if o.Status != "calculated" {
		t.Fatal(o)
	}
	f.IssueDate = "2030-01-01"
	o, _ = EvaluateWithCoverage(f, []Rule{r}, coverage)
	if o.Status != "pending" {
		t.Fatal(o)
	}
	f.Kind = "personal"
	o, _ = EvaluateWithCoverage(f, []Rule{r}, coverage)
	if o.Status != "personal" {
		t.Fatal(o)
	}
}
func TestCalendarAndTimezone(t *testing.T) {
	for _, tc := range []struct {
		in    Date
		years int
		want  Date
	}{{"2024-02-29", 1, "2025-02-28"}, {"2024-01-31", 1, "2025-01-31"}, {"2024-12-31", 1, "2025-12-31"}} {
		got, e := addYears(tc.in, tc.years)
		if e != nil || got != tc.want {
			t.Fatalf("%s got %s %v", tc.in, got, e)
		}
	}
	old := time.Local
	defer func() { time.Local = old }()
	r := synthetic("a", "invoice", "issue_date", 1)
	f := Facts{Kind: "business", Jurisdiction: "FR", Entity: "company", DocumentType: "invoice", IssueDate: "2024-02-29"}
	time.Local = time.FixedZone("west", -11*3600)
	a, _ := EvaluateWithCoverage(f, []Rule{r}, []Coverage{syntheticCoverage("invoice", "invoice", "complete", "2020-01-01", "2030-01-01")})
	time.Local = time.FixedZone("east", 14*3600)
	b, _ := EvaluateWithCoverage(f, []Rule{r}, []Coverage{syntheticCoverage("invoice", "invoice", "complete", "2020-01-01", "2030-01-01")})
	if !reflect.DeepEqual(a, b) {
		t.Fatal(a, b)
	}
}

func TestCoverageExclusionAndBoundaries(t *testing.T) {
	r := synthetic("invoice", "invoice", "issue_date", 2)
	f := Facts{Kind: "business", Jurisdiction: "FR", Entity: "company", DocumentType: "invoice", IssueDate: "2024-01-01"}
	coverage := []Coverage{syntheticCoverage("first", "invoice", "complete", "2020-01-01", "2024-01-01"), syntheticCoverage("second", "invoice", "complete", "2025-01-01", "2030-01-01")}
	for _, date := range []Date{"2024-01-01", "2024-12-31"} {
		f.IssueDate = date
		o, _ := EvaluateWithCoverage(f, []Rule{r}, coverage)
		if o.Status != "pending" || o.LowerBound == "" || o.KeepThrough != "" || !reflect.DeepEqual(o.Reasons, []string{"coverage_gap"}) {
			t.Fatal(date, o)
		}
	}
	f.IssueDate = "2025-01-01"
	o, _ := EvaluateWithCoverage(f, []Rule{r}, coverage)
	if o.Status != "calculated" || o.KeepThrough != "2027-01-01" {
		t.Fatal(o)
	}
	f.IssueDate = ""
	o, _ = EvaluateWithCoverage(f, []Rule{r}, coverage)
	if o.Status != "pending" || o.KeepThrough != "" {
		t.Fatal(o)
	}
	f.DocumentType = "lease"
	f.IssueDate = "2024-01-01"
	exclusion := syntheticCoverage("excluded-lease", "lease", "excluded", "2020-01-01", "2030-01-01")
	o, _ = EvaluateWithCoverage(f, []Rule{r}, []Coverage{exclusion})
	if o.Status != "out_of_scope" || !reflect.DeepEqual(o.Coverage, []Coverage{exclusion}) {
		t.Fatal(o)
	}
}

func TestCoverageOrderDoesNotChangeEvidence(t *testing.T) {
	f := Facts{Kind: "business", Jurisdiction: "FR", Entity: "company", DocumentType: "invoice", IssueDate: "2024-02-29"}
	a, b := syntheticCoverage("a", "invoice", "complete", "2020-01-01", "2026-01-01"), syntheticCoverage("b", "invoice", "complete", "2023-01-01", "2030-01-01")
	rule := synthetic("invoice", "invoice", "issue_date", 1)
	first, err := EvaluateWithCoverage(f, []Rule{rule}, []Coverage{b, a})
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := EvaluateWithCoverage(f, []Rule{rule}, []Coverage{a, b})
	if err != nil || !reflect.DeepEqual(first, reversed) || !reflect.DeepEqual(first.Coverage, []Coverage{a, b}) {
		t.Fatal(first, reversed, err)
	}
}
