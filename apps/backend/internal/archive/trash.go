package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type TrashItem struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	TrashedAt     time.Time `json:"trashedAt"`
	RecoveryUntil time.Time `json:"recoveryUntil"`
	revision      string
}

func (s *Service) MoveToTrash(ctx context.Context, actor int64, archiveID, documentID, name, reason string) (time.Time, error) {
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) {
		return time.Time{}, ErrNotFound
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 1000 {
		return time.Time{}, ErrInvalid
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return time.Time{}, e
	}
	defer tx.Rollback()
	role, _, e := s.membership(ctx, tx, archiveID, actor, "view")
	if e != nil {
		return time.Time{}, e
	}
	if !Can(role, false, "trash") {
		return time.Time{}, ErrForbidden
	}
	if _, e = tx.ExecContext(ctx, `SELECT set_config('invoyis.include_trash','on',true)`); e != nil {
		return time.Time{}, e
	}
	var actual string
	e = tx.QueryRowContext(ctx, `SELECT s.original_filename FROM invoyis.documents d JOIN invoyis.submissions s ON s.id=d.submission_id WHERE d.archive_id=$1 AND d.id=$2`, archiveID, documentID).Scan(&actual)
	if errors.Is(e, sql.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if e != nil {
		return time.Time{}, e
	}
	if name != actual {
		return time.Time{}, ErrConflict
	}
	var deadline time.Time
	e = tx.QueryRowContext(ctx, `SELECT invoyis.move_document_to_trash($1,$2,$3)`, archiveID, documentID, reason).Scan(&deadline)
	if e != nil {
		return time.Time{}, trashSQLError(e)
	}
	return deadline, tx.Commit()
}

func (s *Service) RestoreFromTrash(ctx context.Context, actor int64, archiveID, documentID string) error {
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(documentID) {
		return ErrNotFound
	}
	tx, e := s.transaction(ctx, actor, archiveID)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	role, _, e := s.membership(ctx, tx, archiveID, actor, "view")
	if e != nil {
		return e
	}
	if !Can(role, false, "restore") {
		return ErrForbidden
	}
	if _, e = tx.ExecContext(ctx, `SELECT set_config('invoyis.include_trash','on',true)`); e != nil {
		return e
	}
	var restored bool
	e = tx.QueryRowContext(ctx, `SELECT invoyis.restore_document_from_trash($1,$2)`, archiveID, documentID).Scan(&restored)
	if e != nil {
		return trashSQLError(e)
	}
	return tx.Commit()
}

func trashSQLError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "42501":
			return ErrForbidden
		case "P0002", "02000":
			return ErrNotFound
		case "22023":
			return ErrConflict
		}
	}
	return err
}

func (s *Service) ListTrash(ctx context.Context, actor int64, archiveID string) ([]TrashItem, error) {
	list, err := s.readTrash(ctx, actor, archiveID, nil)
	return list.Items, err
}

type trashList struct {
	Items     []TrashItem       `json:"items"`
	CanShred  bool              `json:"canShred"`
	Shredding []TrashShredEvent `json:"shredding"`
}

func (s *Service) readTrash(ctx context.Context, actor int64, archiveID string, page *trashShredPage) (trashList, error) {
	list := trashList{Items: []TrashItem{}, Shredding: []TrashShredEvent{}}
	if !uuidPattern.MatchString(archiveID) {
		return list, ErrNotFound
	}
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return list, err
	}
	defer tx.Rollback()
	role, _, err := s.membership(ctx, tx, archiveID, actor, "view")
	if err != nil {
		return list, err
	}
	list.CanShred = Can(role, false, "shred")
	if page != nil && !list.CanShred {
		return list, ErrForbidden
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('invoyis.include_trash','on',true)`); err != nil {
		return list, err
	}
	query := `SELECT d.id,s.original_filename,d.trashed_at,d.recovery_until,d.current_confirmed_revision_id
		FROM invoyis.documents d JOIN invoyis.submissions s ON s.id=d.submission_id
		WHERE d.archive_id=$1 AND d.trashed_at IS NOT NULL AND d.shred_requested_at IS NULL`
	args := []any{archiveID}
	limit := 200
	if page != nil {
		limit = trashShredBatchSize + 1
		switch {
		case page.documentID != "":
			query += " AND d.id=$2"
			args = append(args, page.documentID)
		case page.beforeID != "":
			query += " AND (d.trashed_at,d.id)<($2,$3)"
			args = append(args, page.beforeTime, page.beforeID)
		}
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY d.trashed_at DESC,d.id DESC LIMIT $%d", len(args))
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return list, err
	}
	defer rows.Close()
	for rows.Next() {
		var item TrashItem
		var revision sql.NullString
		if err = rows.Scan(&item.ID, &item.Name, &item.TrashedAt, &item.RecoveryUntil, &revision); err != nil {
			return list, err
		}
		item.revision = revision.String
		list.Items = append(list.Items, item)
	}
	if err = rows.Err(); err != nil {
		return list, err
	}
	if page == nil {
		list.Shredding, err = readTrashShredding(ctx, tx, archiveID)
		if err != nil {
			return list, err
		}
	}
	return list, tx.Commit()
}

func (s *Service) moveToTrashHTTP(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var body struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	}
	if e := decodeJSON(w, r, &body); e != nil {
		writeFailure(w, e, requestID)
		return
	}
	deadline, e := s.MoveToTrash(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id"), body.Name, body.Reason)
	if e != nil {
		writeFailure(w, e, requestID)
		return
	}
	writeJSON(w, 200, requestID, map[string]any{"recoveryUntil": deadline})
}
func (s *Service) restoreFromTrashHTTP(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	if e := s.RestoreFromTrash(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("id")); e != nil {
		writeFailure(w, e, requestID)
		return
	}
	w.WriteHeader(204)
}
func (s *Service) listTrashHTTP(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	list, e := s.readTrash(r.Context(), actor, r.PathValue("archiveId"), nil)
	if e != nil {
		writeFailure(w, e, requestID)
		return
	}
	writeJSON(w, 200, requestID, list)
}
