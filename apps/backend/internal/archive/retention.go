package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
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
	// A missing row must serialize with the archive lock held by readers.
	var lockedArchive string
	if e = tx.QueryRowContext(ctx, `SELECT id FROM invoyis.archives WHERE id=$1 FOR UPDATE`, archiveID).Scan(&lockedArchive); e != nil {
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
		if current != expected {
			return TaxYearFacts{}, ErrConflict
		}
		facts, e := loadTaxYear(ctx, tx, archiveID, taxYear)
		if e != nil {
			return facts, e
		}
		return facts, tx.Commit()
	}
	if current != expected {
		return TaxYearFacts{}, ErrConflict
	}
	var old TaxYearFacts
	if current > 0 {
		old, e = loadTaxYear(ctx, tx, archiveID, taxYear)
		if e != nil {
			return TaxYearFacts{}, e
		}
	}
	now := s.Now().UTC()
	verifiedEvent := func(input, prior *TaxYearEvent) *TaxYearEvent {
		if input == nil {
			return nil
		}
		if prior != nil && prior.Date == input.Date && prior.Source == input.Source {
			return prior
		}
		return &TaxYearEvent{Date: input.Date, Source: input.Source, ActorID: actor, RecordedAt: now}
	}
	yearEnd = verifiedEvent(yearEnd, old.FinancialYearEnd)
	filing = verifiedEvent(filing, old.FilingDate)
	makeEvent := func(value *TaxYearEvent) (any, any, any, any) {
		if value == nil {
			return nil, nil, nil, nil
		}
		return string(value.Date), value.Source, value.ActorID, value.RecordedAt
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

// RetentionDocumentFacts records explicit, versioned entity and tax-year assignments.
type RetentionDocumentFacts struct {
	ArchiveID    string    `json:"archiveId"`
	DocumentID   string    `json:"documentId"`
	Version      int64     `json:"version"`
	Entity       string    `json:"entity"`
	TaxYear      int       `json:"taxYear"`
	TaxYearBasis string    `json:"taxYearBasis"`
	ActorID      int64     `json:"actorId"`
	RecordedAt   time.Time `json:"recordedAt"`
}
type CurrentRetentionResult struct {
	State    string             `json:"state"`
	Snapshot *RetentionSnapshot `json:"snapshot,omitempty"`
}

func loadRetentionDocumentFacts(ctx context.Context, tx *sql.Tx, archiveID, documentID string) (RetentionDocumentFacts, error) {
	f := RetentionDocumentFacts{ArchiveID: archiveID, DocumentID: documentID}
	var entity, basis sql.NullString
	var year sql.NullInt64
	e := tx.QueryRowContext(ctx, `SELECT version,entity_scope,tax_year,tax_year_basis,actor_id,recorded_at FROM invoyis.retention_document_facts WHERE archive_id=$1 AND document_id=$2`, archiveID, documentID).Scan(&f.Version, &entity, &year, &basis, &f.ActorID, &f.RecordedAt)
	f.Entity = entity.String
	f.TaxYear = int(year.Int64)
	f.TaxYearBasis = basis.String
	return f, e
}

// SetRetentionDocumentFacts requires an archive admin and a compare-and-swap
// version. Unknown values stay NULL instead of being inferred from upload time.
func (s *Service) SetRetentionDocumentFacts(ctx context.Context, actor int64, archiveID, documentID string, expected int64, entity string, year int, basis string) (RetentionDocumentFacts, error) {
	var f RetentionDocumentFacts
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) || expected < 0 || year < 0 || year > 9999 || (year != 0 && year < 1000) || (year == 0) != (basis == "") {
		return f, ErrInvalid
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return f, e
	}
	defer tx.Rollback()
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "settings"); e != nil {
		return f, e
	}
	var id string
	if e = tx.QueryRowContext(ctx, `SELECT id FROM invoyis.documents WHERE archive_id=$1 AND id=$2 FOR UPDATE`, archiveID, documentID).Scan(&id); e != nil {
		return f, e
	}
	f, e = loadRetentionDocumentFacts(ctx, tx, archiveID, documentID)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return f, e
	}
	if errors.Is(e, sql.ErrNoRows) {
		f = RetentionDocumentFacts{ArchiveID: archiveID, DocumentID: documentID}
	}
	if f.Version > 0 && f.Entity == entity && f.TaxYear == year && f.TaxYearBasis == basis {
		if f.Version != expected {
			return f, ErrConflict
		}
		return f, tx.Commit()
	}
	if f.Version != expected {
		return f, ErrConflict
	}
	if expected == 0 {
		_, e = tx.ExecContext(ctx, `INSERT INTO invoyis.retention_document_facts(archive_id,document_id,entity_scope,tax_year,tax_year_basis,actor_id) VALUES($1,$2,NULLIF($3,''),NULLIF($4,0),NULLIF($5,''),$6)`, archiveID, documentID, entity, year, basis, actor)
	} else {
		_, e = tx.ExecContext(ctx, `UPDATE invoyis.retention_document_facts SET version=version+1,entity_scope=NULLIF($3,''),tax_year=NULLIF($4,0),tax_year_basis=NULLIF($5,''),actor_id=$6,recorded_at=now() WHERE archive_id=$1 AND document_id=$2`, archiveID, documentID, entity, year, basis, actor)
	}
	if e != nil {
		return f, e
	}
	f, e = loadRetentionDocumentFacts(ctx, tx, archiveID, documentID)
	if e != nil {
		return f, e
	}
	data, _ := json.Marshal(f)
	_, e = tx.ExecContext(ctx, `INSERT INTO invoyis.retention_document_facts_history(archive_id,document_id,version,evidence,actor_id) VALUES($1,$2,$3,$4,$5)`, archiveID, documentID, f.Version, data, actor)
	if e != nil {
		return f, e
	}
	return f, tx.Commit()
}

type retentionInputs struct {
	EvaluatorVersion string                  `json:"evaluatorVersion"`
	Facts            retention.Facts         `json:"facts"`
	DocumentFacts    *RetentionDocumentFacts `json:"documentFacts"`
	Events           *TaxYearFacts           `json:"events"`
	Rules            []retention.Rule        `json:"rules"`
	Coverage         []retention.Coverage    `json:"coverage,omitempty"`
}

const retentionEvaluatorVersion = "coverage-v2"

type retentionCandidate struct {
	revision, set string
	input         retentionInputs
	encoded       []byte
	fingerprint   string
	eligible      bool
}

func (s *Service) readRetentionCandidate(ctx context.Context, tx *sql.Tx, archiveID, documentID string) (retentionCandidate, error) {
	var c retentionCandidate
	var kind, country, state, clearance, docType, revision sql.NullString
	var issue sql.NullTime
	e := tx.QueryRowContext(ctx, `SELECT a.kind,a.jurisdiction,d.state,d.clearance,d.current_confirmed_revision_id,r.metadata->>'documentType',r.issue_date FROM invoyis.archives a JOIN invoyis.documents d ON d.archive_id=a.id LEFT JOIN invoyis.document_revisions r ON r.id=d.current_confirmed_revision_id WHERE a.id=$1 AND d.id=$2`, archiveID, documentID).Scan(&kind, &country, &state, &clearance, &revision, &docType, &issue)
	if e != nil {
		return c, e
	}
	if kind.String != "business" || state.String != "ready" || clearance.String != "cleared" || !revision.Valid {
		return c, nil
	}
	c.eligible = true
	c.input.EvaluatorVersion = retentionEvaluatorVersion
	c.revision = revision.String
	e = tx.QueryRowContext(ctx, `SELECT version FROM invoyis.retention_active_rule_set WHERE singleton=true`).Scan(&c.set)
	if e != nil {
		return c, e
	}
	c.input.Facts = retention.Facts{Kind: kind.String, Jurisdiction: country.String, DocumentType: docType.String}
	if issue.Valid {
		c.input.Facts.IssueDate = retention.Date(issue.Time.Format("2006-01-02"))
	}
	f, e := loadRetentionDocumentFacts(ctx, tx, archiveID, documentID)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return c, e
	}
	if e == nil {
		c.input.DocumentFacts = &f
		c.input.Facts.Entity = f.Entity
		c.input.Facts.TaxYear = f.TaxYear
	}
	if c.input.Facts.TaxYear != 0 {
		events, err := loadTaxYear(ctx, tx, archiveID, c.input.Facts.TaxYear)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return c, err
		}
		if err == nil {
			c.input.Events = &events
			if events.FinancialYearEnd != nil {
				c.input.Facts.FinancialYearEnd = events.FinancialYearEnd.Date
			}
			if events.FilingDate != nil {
				c.input.Facts.FilingDate = events.FilingDate.Date
			}
		}
	}
	rows, e := tx.QueryContext(ctx, `SELECT rule_id,version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active FROM invoyis.retention_rule_versions WHERE rule_set_version=$1`, c.set)
	if e != nil {
		return c, e
	}
	c.input.Rules = []retention.Rule{}
	for rows.Next() {
		var r retention.Rule
		var from time.Time
		var until sql.NullTime
		if e = rows.Scan(&r.ID, &r.Version, &r.Jurisdiction, &r.EntityScope, &r.DocumentScope, &from, &until, &r.SelectionDate, &r.Source, &r.Trigger, &r.Years, &r.Active); e != nil {
			rows.Close()
			return c, e
		}
		r.EffectiveFrom = retention.Date(from.Format("2006-01-02"))
		if until.Valid {
			r.EffectiveUntil = retention.Date(until.Time.Format("2006-01-02"))
		}
		c.input.Rules = append(c.input.Rules, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return c, e
	}
	sort.Slice(c.input.Rules, func(i, j int) bool {
		if c.input.Rules[i].ID == c.input.Rules[j].ID {
			return c.input.Rules[i].Version < c.input.Rules[j].Version
		}
		return c.input.Rules[i].ID < c.input.Rules[j].ID
	})
	coverageRows, e := tx.QueryContext(ctx, `SELECT coverage_id,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,status,source_reference FROM invoyis.retention_coverage WHERE rule_set_version=$1 ORDER BY coverage_id`, c.set)
	if e != nil {
		return c, e
	}
	for coverageRows.Next() {
		var declaration retention.Coverage
		var from time.Time
		var until sql.NullTime
		if e = coverageRows.Scan(&declaration.ID, &declaration.Jurisdiction, &declaration.EntityScope, &declaration.DocumentScope, &from, &until, &declaration.SelectionDate, &declaration.Status, &declaration.Source); e != nil {
			coverageRows.Close()
			return c, e
		}
		declaration.EffectiveFrom = retention.Date(from.Format("2006-01-02"))
		if until.Valid {
			declaration.EffectiveUntil = retention.Date(until.Time.Format("2006-01-02"))
		}
		c.input.Coverage = append(c.input.Coverage, declaration)
	}
	e = coverageRows.Err()
	coverageRows.Close()
	if e != nil {
		return c, e
	}
	c.encoded, e = json.Marshal(c.input)
	if e != nil {
		return c, e
	}
	hash := sha256.Sum256(append([]byte(c.set+"/"+c.revision+"/"), c.encoded...))
	c.fingerprint = hex.EncodeToString(hash[:])
	return c, nil
}
func lockRetentionInputs(ctx context.Context, tx *sql.Tx, archiveID, documentID string) error {
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT invoyis.lock_retention_inputs($1,$2)`, archiveID, documentID).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return ErrNotFound
	}
	return nil
}

// EvaluateRetention first computes from a candidate, then locks and rechecks all
// inputs. Its test hook can pause an old candidate before the final recheck.
func (s *Service) EvaluateRetention(ctx context.Context, actor int64, archiveID, documentID string) (RetentionSnapshot, error) {
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
	initial, e := s.readRetentionCandidate(ctx, tx, archiveID, documentID)
	if e != nil {
		return snap, e
	}
	if !initial.eligible {
		return snap, ErrForbidden
	}
	outcome, e := retention.EvaluateWithCoverage(initial.input.Facts, initial.input.Rules, initial.input.Coverage)
	if e != nil {
		return snap, e
	}
	if s.BeforeRetentionPublish != nil {
		s.BeforeRetentionPublish()
	}
	if e = lockRetentionInputs(ctx, tx, archiveID, documentID); e != nil {
		return snap, e
	}
	fresh, e := s.readRetentionCandidate(ctx, tx, archiveID, documentID)
	if e != nil {
		return snap, e
	}
	if !fresh.eligible || fresh.fingerprint != initial.fingerprint || !bytes.Equal(fresh.encoded, initial.encoded) {
		return snap, ErrConflict
	}
	id, e := newID()
	if e != nil {
		return snap, e
	}
	result, _ := json.Marshal(outcome)
	e = tx.QueryRowContext(ctx, `INSERT INTO invoyis.retention_evaluations(id,archive_id,document_id,confirmed_revision_id,fingerprint,rule_set_version,inputs,outcome) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(archive_id,document_id,fingerprint) DO NOTHING RETURNING id`, id, archiveID, documentID, initial.revision, initial.fingerprint, initial.set, initial.encoded, result).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		e = tx.QueryRowContext(ctx, `SELECT id FROM invoyis.retention_evaluations WHERE archive_id=$1 AND document_id=$2 AND fingerprint=$3`, archiveID, documentID, initial.fingerprint).Scan(&id)
	}
	if e != nil {
		return snap, e
	}
	if e = tx.Commit(); e != nil {
		return snap, e
	}
	return RetentionSnapshot{id, archiveID, documentID, initial.revision, initial.set, initial.encoded, outcome}, nil
}

// CurrentRetention only reads. Stale and missing are distinct from a current
// snapshot whose statutory outcome is pending.
func (s *Service) CurrentRetention(ctx context.Context, actor int64, archiveID, documentID string) (CurrentRetentionResult, error) {
	out := CurrentRetentionResult{State: "unavailable"}
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) {
		return out, ErrNotFound
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "view"); e != nil {
		return out, e
	}
	if e = lockRetentionInputs(ctx, tx, archiveID, documentID); e != nil {
		return out, e
	}
	c, e := s.readRetentionCandidate(ctx, tx, archiveID, documentID)
	if e != nil {
		return out, e
	}
	if !c.eligible {
		return out, tx.Commit()
	}
	var id, revision, set string
	var rawInputs, rawOutcome []byte
	e = tx.QueryRowContext(ctx, `SELECT id,confirmed_revision_id,rule_set_version,inputs,outcome FROM invoyis.retention_evaluations WHERE archive_id=$1 AND document_id=$2 AND fingerprint=$3`, archiveID, documentID, c.fingerprint).Scan(&id, &revision, &set, &rawInputs, &rawOutcome)
	if e == nil {
		var outcome retention.Outcome
		if e = json.Unmarshal(rawOutcome, &outcome); e != nil {
			return out, e
		}
		out.State = "current"
		out.Snapshot = &RetentionSnapshot{id, archiveID, documentID, revision, set, rawInputs, outcome}
	} else if errors.Is(e, sql.ErrNoRows) {
		var exists bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM invoyis.retention_evaluations WHERE archive_id=$1 AND document_id=$2)`, archiveID, documentID).Scan(&exists); e != nil {
			return out, e
		}
		if exists {
			out.State = "stale"
		} else {
			out.State = "missing"
		}
	} else {
		return out, e
	}
	return out, tx.Commit()
}

// ReconcileRetention claims durable work. Failed attempts remain leased for retry.
func (s *Service) ReconcileRetention(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 32 {
		return 0, ErrInvalid
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT archive_id,document_id,generation,claim_token,actor_id FROM invoyis.claim_retention_work($1)`, limit)
	if e != nil {
		return 0, e
	}
	type job struct {
		archive, document string
		claimToken        string
		generation, actor int64
	}
	jobs := []job{}
	for rows.Next() {
		var j job
		if e = rows.Scan(&j.archive, &j.document, &j.generation, &j.claimToken, &j.actor); e != nil {
			rows.Close()
			return 0, e
		}
		jobs = append(jobs, j)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	done := 0
	var firstError error
	for _, j := range jobs {
		if s.BeforeRetentionJob != nil {
			s.BeforeRetentionJob()
		}
		_, err := s.EvaluateRetention(ctx, j.actor, j.archive, j.document)
		// A selected admin may have lost membership since claim. Keep the job for
		// another admin after its lease expires.
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil && !errors.Is(err, ErrForbidden) {
			if !errors.Is(err, ErrConflict) && firstError == nil {
				firstError = err
			}
			continue
		}
		var acknowledged bool
		if e = s.DB.QueryRowContext(ctx, `SELECT invoyis.ack_retention_work($1,$2,$3)`, j.archive, j.document, j.claimToken).Scan(&acknowledged); e == nil && acknowledged {
			done++
		}
	}
	return done, firstError
}
func (s *Service) RunRetentionReconciler(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.ReconcileRetention(ctx, 16); err != nil {
				log.Printf("retention reconciliation failed: %v", err)
			}
		}
	}
}
