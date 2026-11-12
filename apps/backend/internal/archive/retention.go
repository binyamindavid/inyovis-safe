package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/retention"
)

// TaxYearEvent carries a calendar date and its independently recorded provenance.
type TaxYearEvent struct {
	Date       retention.Date `json:"date"`
	Source     string         `json:"source"`
	ActorID    int64          `json:"actorId"`
	RecordedAt time.Time      `json:"recordedAt"`
}
type TaxYearFacts struct {
	ArchiveID        string        `json:"archiveId"`
	TaxYear          int           `json:"taxYear"`
	Version          int64         `json:"version"`
	FinancialYearEnd *TaxYearEvent `json:"financialYearEnd,omitempty"`
	FilingDate       *TaxYearEvent `json:"filingDate,omitempty"`
}

func validEvent(e *TaxYearEvent) bool {
	if e == nil {
		return true
	}
	if e.Source == "" || e.Date == "" {
		return false
	}
	_, err := time.Parse("2006-01-02", string(e.Date))
	return err == nil
}

// PutTaxYearFacts uses compare-and-swap. A replay of the current values is harmless;
// a correction with an old version conflicts, even when another admin wrote it.
func (s *Service) PutTaxYearFacts(ctx context.Context, actor int64, archiveID string, taxYear int, expected int64, yearEnd, filing *TaxYearEvent) (TaxYearFacts, error) {
	if !uuidPattern.MatchString(archiveID) || taxYear < 1000 || taxYear > 9999 || expected < 0 || !validEvent(yearEnd) || !validEvent(filing) {
		return TaxYearFacts{}, ErrInvalid
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return TaxYearFacts{}, e
	}
	defer tx.Rollback()
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "settings"); e != nil {
		return TaxYearFacts{}, e
	}
	var current int64
	var oldEnd, oldFiling sql.NullTime
	var endSource, filingSource sql.NullString
	e = tx.QueryRowContext(ctx, `SELECT version,financial_year_end,filing_date,financial_year_end_source,filing_date_source FROM invoyis.archive_tax_years WHERE archive_id=$1 AND tax_year=$2 FOR UPDATE`, archiveID, taxYear).Scan(&current, &oldEnd, &oldFiling, &endSource, &filingSource)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return TaxYearFacts{}, e
	}
	if errors.Is(e, sql.ErrNoRows) {
		current = 0
	}
	same := func(old sql.NullTime, source sql.NullString, value *TaxYearEvent) bool {
		if value == nil {
			return !old.Valid
		}
		return old.Valid && old.Time.Format("2006-01-02") == string(value.Date) && source.String == value.Source
	}
	if current > 0 && same(oldEnd, endSource, yearEnd) && same(oldFiling, filingSource, filing) {
		facts, e := loadTaxYear(ctx, tx, archiveID, taxYear)
		if e != nil {
			return facts, e
		}
		return facts, tx.Commit()
	}
	if current != expected {
		return TaxYearFacts{}, ErrConflict
	}
	now := s.Now().UTC()
	makeEvent := func(value *TaxYearEvent) (any, any, any, any) {
		if value == nil {
			return nil, nil, nil, nil
		}
		return string(value.Date), value.Source, actor, now
	}
	ed, es, ea, et := makeEvent(yearEnd)
	fd, fs, fa, ft := makeEvent(filing)
	if current == 0 {
		var result sql.Result
		result, e = tx.ExecContext(ctx, `INSERT INTO invoyis.archive_tax_years(archive_id,tax_year,version,financial_year_end,financial_year_end_source,financial_year_end_actor,financial_year_end_recorded_at,filing_date,filing_date_source,filing_date_actor,filing_date_recorded_at) VALUES($1,$2,1,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`, archiveID, taxYear, ed, es, ea, et, fd, fs, fa, ft)
		if e == nil {
			affected, _ := result.RowsAffected()
			if affected == 0 {
				return TaxYearFacts{}, ErrConflict
			}
		}
	} else {
		_, e = tx.ExecContext(ctx, `UPDATE invoyis.archive_tax_years SET version=version+1,financial_year_end=$3,financial_year_end_source=$4,financial_year_end_actor=$5,financial_year_end_recorded_at=$6,filing_date=$7,filing_date_source=$8,filing_date_actor=$9,filing_date_recorded_at=$10 WHERE archive_id=$1 AND tax_year=$2`, archiveID, taxYear, ed, es, ea, et, fd, fs, fa, ft)
	}
	if e != nil {
		return TaxYearFacts{}, e
	}
	facts, e := loadTaxYear(ctx, tx, archiveID, taxYear)
	if e != nil {
		return facts, e
	}
	if facts.Version != current+1 {
		return TaxYearFacts{}, ErrConflict
	}
	evidence, _ := json.Marshal(facts)
	_, e = tx.ExecContext(ctx, `INSERT INTO invoyis.archive_tax_year_history(archive_id,tax_year,version,evidence,actor_id) VALUES($1,$2,$3,$4,$5)`, archiveID, taxYear, facts.Version, evidence, actor)
	if e != nil {
		return TaxYearFacts{}, e
	}
	return facts, tx.Commit()
}
func loadTaxYear(ctx context.Context, tx *sql.Tx, archiveID string, year int) (TaxYearFacts, error) {
	var f TaxYearFacts
	f.ArchiveID = archiveID
	f.TaxYear = year
	var ed, fd sql.NullTime
	var es, fs sql.NullString
	var ea, fa sql.NullInt64
	var et, ft sql.NullTime
	e := tx.QueryRowContext(ctx, `SELECT version,financial_year_end,financial_year_end_source,financial_year_end_actor,financial_year_end_recorded_at,filing_date,filing_date_source,filing_date_actor,filing_date_recorded_at FROM invoyis.archive_tax_years WHERE archive_id=$1 AND tax_year=$2`, archiveID, year).Scan(&f.Version, &ed, &es, &ea, &et, &fd, &fs, &fa, &ft)
	if e != nil {
		return f, e
	}
	if ed.Valid {
		f.FinancialYearEnd = &TaxYearEvent{retention.Date(ed.Time.Format("2006-01-02")), es.String, ea.Int64, et.Time}
	}
	if fd.Valid {
		f.FilingDate = &TaxYearEvent{retention.Date(fd.Time.Format("2006-01-02")), fs.String, fa.Int64, ft.Time}
	}
	return f, nil
}

type RetentionSnapshot struct {
	ID                  string            `json:"id"`
	ArchiveID           string            `json:"archiveId"`
	DocumentID          string            `json:"documentId"`
	ConfirmedRevisionID string            `json:"confirmedRevisionId"`
	RuleSetVersion      string            `json:"ruleSetVersion"`
	Inputs              json.RawMessage   `json:"inputs"`
	Outcome             retention.Outcome `json:"outcome"`
}

// EvaluateRetention reads and publishes under archive and document locks. Existing
// confirmed documents enter through this same path; drafts never enter the input.
func (s *Service) EvaluateRetention(ctx context.Context, actor int64, archiveID, documentID, entity string, taxYear int) (RetentionSnapshot, error) {
	var snap RetentionSnapshot
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) {
		return snap, ErrNotFound
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return snap, e
	}
	defer tx.Rollback()
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "view"); e != nil {
		return snap, e
	}
	var kind, country, state, clearance, revision, docType string
	var issue sql.NullTime
	e = tx.QueryRowContext(ctx, `SELECT a.kind,COALESCE(a.jurisdiction,''),d.state,d.clearance,d.current_confirmed_revision_id,r.metadata->>'documentType',r.issue_date FROM invoyis.archives a JOIN invoyis.documents d ON d.archive_id=a.id JOIN invoyis.document_revisions r ON r.id=d.current_confirmed_revision_id WHERE a.id=$1 AND d.id=$2 FOR SHARE OF a,d`, archiveID, documentID).Scan(&kind, &country, &state, &clearance, &revision, &docType, &issue)
	if e != nil {
		return snap, e
	}
	if state != "ready" || clearance != "cleared" {
		return snap, ErrForbidden
	}
	var set string
	e = tx.QueryRowContext(ctx, `SELECT version FROM invoyis.retention_rule_sets ORDER BY created_at DESC,version DESC LIMIT 1`).Scan(&set)
	if e != nil {
		return snap, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT rule_id,version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active FROM invoyis.retention_rule_versions WHERE rule_set_version=$1`, set)
	if e != nil {
		return snap, e
	}
	rules := []retention.Rule{}
	for rows.Next() {
		var r retention.Rule
		var from time.Time
		var until sql.NullTime
		e = rows.Scan(&r.ID, &r.Version, &r.Jurisdiction, &r.EntityScope, &r.DocumentScope, &from, &until, &r.SelectionDate, &r.Source, &r.Trigger, &r.Years, &r.Active)
		if e != nil {
			rows.Close()
			return snap, e
		}
		r.EffectiveFrom = retention.Date(from.Format("2006-01-02"))
		if until.Valid {
			r.EffectiveUntil = retention.Date(until.Time.Format("2006-01-02"))
		}
		rules = append(rules, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return snap, e
	}
	facts := retention.Facts{Kind: kind, Jurisdiction: country, Entity: entity, DocumentType: docType, TaxYear: taxYear}
	if issue.Valid {
		facts.IssueDate = retention.Date(issue.Time.Format("2006-01-02"))
	}
	var event any
	if taxYear != 0 {
		f, err := loadTaxYear(ctx, tx, archiveID, taxYear)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return snap, err
		}
		if err == nil {
			event = f
			if f.FinancialYearEnd != nil {
				facts.FinancialYearEnd = f.FinancialYearEnd.Date
			}
			if f.FilingDate != nil {
				facts.FilingDate = f.FilingDate.Date
			}
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].ID == rules[j].ID {
			return rules[i].Version < rules[j].Version
		}
		return rules[i].ID < rules[j].ID
	})
	outcome, e := retention.Evaluate(facts, rules)
	if e != nil {
		return snap, e
	}
	inputs, _ := json.Marshal(struct {
		Facts  retention.Facts  `json:"facts"`
		Events any              `json:"events"`
		Rules  []retention.Rule `json:"rules"`
	}{facts, event, rules})
	digest := sha256.Sum256(append([]byte(set+"/"+revision+"/"), inputs...))
	fingerprint := hex.EncodeToString(digest[:])
	id, e := newID()
	if e != nil {
		return snap, e
	}
	result, _ := json.Marshal(outcome)
	e = tx.QueryRowContext(ctx, `INSERT INTO invoyis.retention_evaluations(id,archive_id,document_id,confirmed_revision_id,fingerprint,rule_set_version,inputs,outcome) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(archive_id,document_id,fingerprint) DO NOTHING RETURNING id`, id, archiveID, documentID, revision, fingerprint, set, inputs, result).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		e = tx.QueryRowContext(ctx, `SELECT id FROM invoyis.retention_evaluations WHERE archive_id=$1 AND document_id=$2 AND fingerprint=$3`, archiveID, documentID, fingerprint).Scan(&id)
	}
	if e != nil {
		return snap, e
	}
	if e = tx.Commit(); e != nil {
		return snap, e
	}
	return RetentionSnapshot{id, archiveID, documentID, revision, set, inputs, outcome}, nil
}

// CurrentRetention returns a snapshot only if all current input versions still match.
// The caller must supply the same explicit entity and tax-year assignment used to evaluate.
func (s *Service) CurrentRetention(ctx context.Context, actor int64, archiveID, documentID, entity string, taxYear int) (RetentionSnapshot, error) {
	snap, e := s.EvaluateRetention(ctx, actor, archiveID, documentID, entity, taxYear)
	if e != nil {
		return snap, e
	}
	return snap, nil
}
