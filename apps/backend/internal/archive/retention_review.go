package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"
)

type DocumentHold struct {
	ID            string     `json:"id"`
	Reason        string     `json:"reason"`
	PlacedBy      int64      `json:"placedBy"`
	PlacedAt      time.Time  `json:"placedAt"`
	ReleasedBy    *int64     `json:"releasedBy,omitempty"`
	ReleasedAt    *time.Time `json:"releasedAt,omitempty"`
	ReleaseReason *string    `json:"releaseReason,omitempty"`
}

type RetentionDetails struct {
	Current       CurrentRetentionResult `json:"current"`
	History       []RetentionSnapshot    `json:"history"`
	Holds         []DocumentHold         `json:"holds"`
	Reminder      *string                `json:"reminder,omitempty"`
	ReviewBlocker string                 `json:"reviewBlocker"`
	CanManage     bool                   `json:"canManage"`
	CanEdit       bool                   `json:"canEdit"`
}

func (s *Service) RetentionDetails(ctx context.Context, actor int64, archiveID, documentID string) (RetentionDetails, error) {
	out := RetentionDetails{History: []RetentionSnapshot{}, Holds: []DocumentHold{}}
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) {
		return out, ErrNotFound
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	role, _, e := s.membership(ctx, tx, archiveID, actor, "view")
	if e != nil {
		return out, e
	}
	out.CanManage = role == "admin"
	out.CanEdit = role == "admin" || role == "contributor"
	if e = lockRetentionInputs(ctx, tx, archiveID, documentID); e != nil {
		return out, e
	}
	c, e := s.readRetentionCandidate(ctx, tx, archiveID, documentID)
	if e != nil {
		return out, e
	}
	out.Current.State = "unavailable"
	rows, e := tx.QueryContext(ctx, `SELECT id,confirmed_revision_id,rule_set_version,inputs,outcome,fingerprint FROM invoyis.retention_evaluations WHERE archive_id=$1 AND document_id=$2 ORDER BY created_at DESC,id DESC`, archiveID, documentID)
	if e != nil {
		return out, e
	}
	current := false
	for rows.Next() {
		var snap RetentionSnapshot
		var raw []byte
		var fingerprint string
		if e = rows.Scan(&snap.ID, &snap.ConfirmedRevisionID, &snap.RuleSetVersion, &snap.Inputs, &raw, &fingerprint); e != nil {
			break
		}
		snap.ArchiveID = archiveID
		snap.DocumentID = documentID
		if e = json.Unmarshal(raw, &snap.Outcome); e != nil {
			break
		}
		if c.eligible && fingerprint == c.fingerprint {
			out.Current.State = "current"
			out.Current.Snapshot = &snap
			current = true
		}
		out.History = append(out.History, snap)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return out, e
	}
	if c.eligible && !current {
		if len(out.History) > 0 {
			out.Current.State = "stale"
			out.Current.Snapshot = &out.History[0]
			previous := out.History[0]
			if previous.ConfirmedRevisionID != c.revision {
				out.Current.StaleReasons = append(out.Current.StaleReasons, "new_confirmed_revision")
			}
			if previous.RuleSetVersion != c.set {
				out.Current.StaleReasons = append(out.Current.StaleReasons, "published_rule_version")
			}
			var oldInputs retentionInputs
			if json.Unmarshal(previous.Inputs, &oldInputs) == nil {
				if oldInputs.Events != nil && c.input.Events != nil && !reflect.DeepEqual(oldInputs.Events.FilingDate, c.input.Events.FilingDate) {
					out.Current.StaleReasons = append(out.Current.StaleReasons, "corrected_filing_date")
				} else if !reflect.DeepEqual(oldInputs.Facts, c.input.Facts) || !reflect.DeepEqual(oldInputs.DocumentFacts, c.input.DocumentFacts) || !reflect.DeepEqual(oldInputs.Events, c.input.Events) {
					out.Current.StaleReasons = append(out.Current.StaleReasons, "corrected_facts_or_events")
				}
			}
		} else {
			out.Current.State = "missing"
		}
	}
	holds, e := tx.QueryContext(ctx, `SELECT id,reason,placed_by,placed_at,released_by,released_at,release_reason FROM invoyis.document_holds WHERE archive_id=$1 AND document_id=$2 ORDER BY placed_at DESC,id DESC`, archiveID, documentID)
	if e != nil {
		return out, e
	}
	active := 0
	for holds.Next() {
		var h DocumentHold
		var by sql.NullInt64
		var at sql.NullTime
		var releaseReason sql.NullString
		if e = holds.Scan(&h.ID, &h.Reason, &h.PlacedBy, &h.PlacedAt, &by, &at, &releaseReason); e != nil {
			break
		}
		if by.Valid {
			h.ReleasedBy = &by.Int64
		}
		if at.Valid {
			h.ReleasedAt = &at.Time
			if releaseReason.Valid { h.ReleaseReason = &releaseReason.String }
		} else {
			active++
		}
		out.Holds = append(out.Holds, h)
	}
	if e == nil {
		e = holds.Err()
	}
	holds.Close()
	if e != nil {
		return out, e
	}
	var kind string
	e = tx.QueryRowContext(ctx, `SELECT kind FROM invoyis.archives WHERE id=$1`, archiveID).Scan(&kind)
	if e != nil {
		return out, e
	}
	if kind == "personal" {
		var date sql.NullTime
		e = tx.QueryRowContext(ctx, `SELECT reminder_date FROM invoyis.personal_reminders WHERE archive_id=$1 AND document_id=$2`, archiveID, documentID).Scan(&date)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if date.Valid {
			v := date.Time.Format("2006-01-02")
			out.Reminder = &v
		}
	}
	out.ReviewBlocker = reviewBlocker(kind, role, c.eligible, out.Current, active, s.Now())
	return out, tx.Commit()
}

func reviewBlocker(kind, role string, eligible bool, current CurrentRetentionResult, active int, now time.Time) string {
	if kind != "business" {
		return "business_document_required"
	}
	if role != "admin" {
		return "reviewer_permission_required"
	}
	if !eligible {
		return "cleared_confirmed_document_required"
	}
	if current.State != "current" {
		return "retention_" + current.State
	}
	outcome := current.Snapshot.Outcome
	if outcome.Status != "calculated" || outcome.KeepThrough == "" {
		return "retention_unresolved"
	}
	if active > 0 {
		return "active_hold"
	}
	keep, e := time.Parse("2006-01-02", string(outcome.KeepThrough))
	if e != nil {
		return "retention_unresolved"
	}
	today, e := time.Parse("2006-01-02", now.UTC().Format("2006-01-02"))
	if e != nil || !today.After(keep) {
		return "keep_through_date"
	}
	return ""
}

func (s *Service) PlaceDocumentHold(ctx context.Context, actor int64, archiveID, documentID, reason string) (DocumentHold, error) {
	var out DocumentHold
	reason = strings.TrimSpace(reason)
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) {
		return out, ErrNotFound
	}
	if len(reason) < 1 || len(reason) > 1000 {
		return out, ErrInvalid
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "settings"); e != nil {
		return out, e
	}
	id, e := newID()
	if e != nil {
		return out, e
	}
	e = tx.QueryRowContext(ctx, `INSERT INTO invoyis.document_holds(id,archive_id,document_id,reason,placed_by) VALUES($1,$2,$3,$4,$5) RETURNING id,reason,placed_by,placed_at`, id, archiveID, documentID, reason, actor).Scan(&out.ID, &out.Reason, &out.PlacedBy, &out.PlacedAt)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Service) ReleaseDocumentHold(ctx context.Context, actor int64, archiveID, documentID, holdID, reason string) error {
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) || !uuidPattern.MatchString(holdID) {
		return ErrNotFound
	}
	reason = strings.TrimSpace(reason)
	if len(reason) < 1 || len(reason) > 1000 {
		return ErrInvalid
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "settings"); e != nil {
		return e
	}
	result, e := tx.ExecContext(ctx, `UPDATE invoyis.document_holds SET released_by=$4,released_at=now(),release_reason=$5 WHERE archive_id=$1 AND document_id=$2 AND id=$3 AND released_at IS NULL`, archiveID, documentID, holdID, actor, reason)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}
func (s *Service) SetPersonalReminder(ctx context.Context, actor int64, archiveID, documentID string, date *string) error {
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) {
		return ErrNotFound
	}
	if date != nil {
		parsed, e := time.Parse("2006-01-02", *date)
		if e != nil || parsed.Format("2006-01-02") != *date {
			return ErrInvalid
		}
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, _, e = s.membership(ctx, tx, archiveID, actor, "correct"); e != nil {
		return e
	}
	var kind string
	e = tx.QueryRowContext(ctx, `SELECT kind FROM invoyis.archives WHERE id=$1`, archiveID).Scan(&kind)
	if e != nil {
		return e
	}
	if kind != "personal" {
		return ErrInvalid
	}
	if date == nil {
		_, e = tx.ExecContext(ctx, `DELETE FROM invoyis.personal_reminders WHERE archive_id=$1 AND document_id=$2`, archiveID, documentID)
	} else {
		_, e = tx.ExecContext(ctx, `INSERT INTO invoyis.personal_reminders(archive_id,document_id,reminder_date,actor_id) VALUES($1,$2,$3,$4) ON CONFLICT(archive_id,document_id) DO UPDATE SET reminder_date=EXCLUDED.reminder_date,actor_id=EXCLUDED.actor_id,updated_at=now()`, archiveID, documentID, *date, actor)
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) OpenShreddingReview(ctx context.Context, actor int64, archiveID, documentID string) (RetentionDetails, error) {
	details, e := s.RetentionDetails(ctx, actor, archiveID, documentID)
	if e != nil {
		return details, e
	}
	if details.ReviewBlocker == "reviewer_permission_required" {
		return details, ErrForbidden
	}
	if details.ReviewBlocker != "" {
		return details, ErrConflict
	}
	return details, nil
}
