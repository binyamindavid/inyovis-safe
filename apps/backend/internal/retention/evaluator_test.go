package retention

import (
	"reflect"
	"testing"
	"time"
)

func synthetic(id, scope, trigger string, years int) Rule {
	return Rule{ID: id, Version: 1, Jurisdiction: "FR", EntityScope: "company", DocumentScope: scope, EffectiveFrom: "2020-01-01", EffectiveUntil: "2030-01-01", SelectionDate: "issue_date", Source: "synthetic", Trigger: trigger, Years: years, Active: true}
}
func TestDeterministicOverlapAndMissingFiling(t *testing.T) {
	a := synthetic("a", "invoice", "financial_year_end", 3)
	b := synthetic("b", "invoice", "filing_date", 5)
	f := Facts{Kind: "business", Jurisdiction: "FR", Entity: "company", DocumentType: "invoice", IssueDate: "2024-02-29", TaxYear: 2024, FinancialYearEnd: "2024-02-29"}
	first, e := Evaluate(f, []Rule{b, a})
	if e != nil || first.Status != "pending" || first.KeepThrough != "" || first.LowerBound != "2027-02-28" {
		t.Fatalf("%+v %v", first, e)
	}
	f.FilingDate = "2025-01-31"
	want, e := Evaluate(f, []Rule{b, a})
	if e != nil || want.Status != "calculated" || want.KeepThrough != "2030-01-31" {
		t.Fatalf("%+v %v", want, e)
	}
	reversed, _ := Evaluate(f, []Rule{a, b})
	if !reflect.DeepEqual(want, reversed) {
		t.Fatal("rule order changed result")
	}
	old, _ := Evaluate(Facts{Kind: "business", Jurisdiction: "FR", Entity: "company", DocumentType: "invoice", IssueDate: "2024-02-29", TaxYear: 2024, FinancialYearEnd: "2024-02-29"}, []Rule{a, b})
	if !reflect.DeepEqual(first, old) {
		t.Fatal("historical inputs changed")
	}
}
func TestScopeCoverageAndWindow(t *testing.T) {
	r := synthetic("invoice", "invoice", "issue_date", 1)
	f := Facts{Kind: "business", Jurisdiction: "FR", Entity: "company", DocumentType: "lease", IssueDate: "2024-01-01"}
	for _, typ := range []string{"lease", "court_correspondence", "other"} {
		f.DocumentType = typ
		o, _ := Evaluate(f, []Rule{r})
		if o.Status != "out_of_scope" || o.KeepThrough != "" {
			t.Fatalf("%s: %+v", typ, o)
		}
	}
	f.DocumentType = ""
	o, _ := Evaluate(f, []Rule{r})
	if o.Status != "pending" {
		t.Fatal(o)
	}
	f.DocumentType = "invoice"
	f.Entity = ""
	o, _ = Evaluate(f, []Rule{r})
	if o.Status != "pending" {
		t.Fatal(o)
	}
	f.Entity = "company"
	f.Jurisdiction = "IL"
	o, _ = Evaluate(f, []Rule{r})
	if o.Status != "pending" {
		t.Fatal(o)
	}
	f.Jurisdiction = "FR"
	f.IssueDate = "2020-01-01"
	o, _ = Evaluate(f, []Rule{r})
	if o.Status != "calculated" {
		t.Fatal(o)
	}
	f.IssueDate = "2030-01-01"
	o, _ = Evaluate(f, []Rule{r})
	if o.Status != "out_of_scope" {
		t.Fatal(o)
	}
	f.Kind = "personal"
	o, _ = Evaluate(f, []Rule{r})
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
	a, _ := Evaluate(f, []Rule{r})
	time.Local = time.FixedZone("east", 14*3600)
	b, _ := Evaluate(f, []Rule{r})
	if !reflect.DeepEqual(a, b) {
		t.Fatal(a, b)
	}
}
