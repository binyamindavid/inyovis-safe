// Package retention evaluates immutable statutory rule versions without a database or clock.
package retention

import (
	"fmt"
	"sort"
	"time"
)

type Date string // ISO 8601 calendar date, never an instant.
func (d Date) parse() (time.Time, error) {
	t, e := time.Parse("2006-01-02", string(d))
	if e != nil || t.Format("2006-01-02") != string(d) {
		return t, fmt.Errorf("invalid calendar date %q", d)
	}
	return t, nil
}
func addYears(d Date, n int) (Date, error) {
	t, e := d.parse()
	if e != nil {
		return "", e
	}
	y, m, day := t.Date()
	target := time.Date(y+n, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > target {
		day = target
	}
	return Date(time.Date(y+n, m, day, 0, 0, 0, 0, time.UTC).Format("2006-01-02")), nil
}

type Rule struct {
	ID             string `json:"id"`
	Version        int    `json:"version"`
	Jurisdiction   string `json:"jurisdiction"`
	EntityScope    string `json:"entityScope"`
	DocumentScope  string `json:"documentScope"`
	EffectiveFrom  Date   `json:"effectiveFrom"`
	EffectiveUntil Date   `json:"effectiveUntil,omitempty"` // exclusive
	SelectionDate  string `json:"selectionDate"`
	Source         string `json:"source"`
	Trigger        string `json:"trigger"`
	Years          int    `json:"years"`
	Active         bool   `json:"active"`
}
type Facts struct {
	Kind             string `json:"kind"`
	Jurisdiction     string `json:"jurisdiction"`
	Entity           string `json:"entity"`
	DocumentType     string `json:"documentType"`
	IssueDate        Date   `json:"issueDate"`
	TaxYear          int    `json:"taxYear"`
	FinancialYearEnd Date   `json:"financialYearEnd"`
	FilingDate       Date   `json:"filingDate"`
}
type Obligation struct {
	RuleID      string         `json:"ruleId"`
	RuleVersion int            `json:"ruleVersion"`
	Source      string         `json:"source"`
	Trigger     string         `json:"trigger"`
	Inputs      map[string]any `json:"inputs"`
	KeepThrough Date           `json:"keepThrough,omitempty"`
	Pending     string         `json:"pending,omitempty"`
}
type Outcome struct {
	Status      string       `json:"status"`
	KeepThrough Date         `json:"keepThrough,omitempty"`
	LowerBound  Date         `json:"lowerBound,omitempty"`
	Reasons     []string     `json:"reasons,omitempty"`
	Obligations []Obligation `json:"obligations"`
}

func Validate(r Rule) error {
	if r.ID == "" || r.Version < 1 || r.Jurisdiction == "" || r.DocumentScope == "" || r.EntityScope == "" || r.Source == "" || r.Years < 0 {
		return fmt.Errorf("incomplete rule")
	}
	if r.SelectionDate != "issue_date" && r.SelectionDate != "financial_year_end" {
		return fmt.Errorf("invalid selection date")
	}
	if r.Trigger != "issue_date" && r.Trigger != "financial_year_end" && r.Trigger != "filing_date" {
		return fmt.Errorf("invalid trigger")
	}
	start, e := r.EffectiveFrom.parse()
	if e != nil {
		return e
	}
	if r.EffectiveUntil != "" {
		end, e := r.EffectiveUntil.parse()
		if e != nil {
			return e
		}
		if !end.After(start) {
			return fmt.Errorf("empty effective window")
		}
	}
	return nil
}
func Evaluate(f Facts, rules []Rule) (Outcome, error) {
	out := Outcome{Status: "pending", Obligations: []Obligation{}, Reasons: []string{}}
	if f.Kind == "personal" {
		out.Status = "personal"
		return out, nil
	}
	if f.Kind != "business" {
		out.Reasons = append(out.Reasons, "archive_kind")
		return out, nil
	}
	if f.Jurisdiction == "" {
		out.Reasons = append(out.Reasons, "jurisdiction")
		return out, nil
	}
	ordered := append([]Rule(nil), rules...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].ID == ordered[j].ID {
			return ordered[i].Version < ordered[j].Version
		}
		return ordered[i].ID < ordered[j].ID
	})
	coverage := false
	possible := false
	for _, r := range ordered {
		if e := Validate(r); e != nil {
			return out, e
		}
		if !r.Active || r.Jurisdiction != f.Jurisdiction {
			continue
		}
		coverage = true
		if f.DocumentType == "" {
			possible = true
			out.Reasons = append(out.Reasons, "document_type")
			continue
		}
		if r.DocumentScope != "*" && r.DocumentScope != f.DocumentType {
			continue
		}
		if f.Entity == "" && r.EntityScope != "*" {
			possible = true
			out.Reasons = append(out.Reasons, "entity")
			continue
		}
		if r.EntityScope != "*" && r.EntityScope != f.Entity {
			continue
		}
		var selectDate Date
		if r.SelectionDate == "issue_date" {
			selectDate = f.IssueDate
		} else {
			selectDate = f.FinancialYearEnd
		}
		if selectDate == "" {
			possible = true
			out.Reasons = append(out.Reasons, r.SelectionDate)
			continue
		}
		sd, _ := selectDate.parse()
		from, _ := r.EffectiveFrom.parse()
		if sd.Before(from) {
			continue
		}
		if r.EffectiveUntil != "" {
			until, _ := r.EffectiveUntil.parse()
			if !sd.Before(until) {
				continue
			}
		}
		possible = true
		ob := Obligation{RuleID: r.ID, RuleVersion: r.Version, Source: r.Source, Trigger: r.Trigger, Inputs: map[string]any{r.SelectionDate: selectDate}}
		var trigger Date
		switch r.Trigger {
		case "issue_date":
			trigger = f.IssueDate
		case "financial_year_end":
			trigger = f.FinancialYearEnd
		case "filing_date":
			trigger = f.FilingDate
		}
		if r.Trigger != "issue_date" && f.TaxYear == 0 {
			ob.Pending = "tax_year"
		} else if trigger == "" {
			ob.Pending = r.Trigger
		} else {
			ob.Inputs[r.Trigger] = trigger
			var err error
			ob.KeepThrough, err = addYears(trigger, r.Years)
			if err != nil {
				return out, err
			}
			if ob.KeepThrough > out.LowerBound {
				out.LowerBound = ob.KeepThrough
			}
		}
		if ob.Pending != "" {
			out.Reasons = append(out.Reasons, ob.Pending)
		}
		out.Obligations = append(out.Obligations, ob)
	}
	if !coverage {
		out.Reasons = append(out.Reasons, "rule_coverage")
	}
	if len(out.Reasons) == 0 {
		if possible {
			out.Status = "calculated"
			out.KeepThrough = out.LowerBound
		} else {
			out.Status = "out_of_scope"
		}
	}
	sort.Strings(out.Reasons)
	return out, nil
}
