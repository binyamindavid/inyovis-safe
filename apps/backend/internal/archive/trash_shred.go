package archive

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

type TrashShredTarget struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TrashedAt time.Time `json:"trashedAt"`
	Revision  string    `json:"revision"`
}

type trashShredCandidate struct {
	TrashShredTarget
	ShredStatus
}

type trashShredPreview struct {
	Items    []trashShredCandidate `json:"items"`
	Eligible int                   `json:"eligible"`
	Blocked  int                   `json:"blocked"`
}

type TrashShredEvent struct {
	ID string `json:"id"`
	ShredStatus
}

func readTrashShredding(ctx context.Context, tx *sql.Tx, archiveID string) ([]TrashShredEvent, error) {
	// Keep the audit reference and timestamps after finalization erases the filename.
	rows, err := tx.QueryContext(ctx, `SELECT document_id,status,requested_at,verified_at,last_error
		FROM invoyis.document_shreds WHERE archive_id=$1 AND evidence->>'fromTrash'='true'
		ORDER BY requested_at DESC,document_id DESC`, archiveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []TrashShredEvent{}
	for rows.Next() {
		var event TrashShredEvent
		if err := rows.Scan(&event.ID, &event.Status, &event.RequestedAt, &event.VerifiedAt, &event.LastError); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Service) previewTrashShred(ctx context.Context, actor int64, archiveID, documentID string) (trashShredPreview, error) {
	preview := trashShredPreview{Items: []trashShredCandidate{}}
	list, err := s.readTrash(ctx, actor, archiveID, true)
	if err != nil {
		return preview, err
	}
	for _, item := range list.Items {
		if documentID != "" && documentID != item.ID {
			continue
		}
		target := TrashShredTarget{ID: item.ID, Name: item.Name, TrashedAt: item.TrashedAt, Revision: item.revision}
		status, err := s.shred(ctx, actor, archiveID, item.ID, item.Name, false, &target)
		if errors.Is(err, ErrNotFound) {
			status = ShredStatus{Status: "blocked", Blocker: "document_changed"}
		} else if err != nil {
			return preview, err
		}
		if status.Status == "eligible" {
			preview.Eligible++
		} else {
			preview.Blocked++
		}
		preview.Items = append(preview.Items, trashShredCandidate{target, status})
	}
	if documentID != "" && len(preview.Items) == 0 {
		return preview, ErrNotFound
	}
	return preview, nil
}

func (s *Service) authorizeTrashShred(ctx context.Context, actor int64, archiveID string, targets []TrashShredTarget) ([]TrashShredEvent, error) {
	if !uuidPattern.MatchString(archiveID) {
		return nil, ErrNotFound
	}
	if len(targets) == 0 {
		return nil, ErrInvalid
	}
	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		if !uuidPattern.MatchString(target.ID) || !uuidPattern.MatchString(target.Revision) || target.TrashedAt.IsZero() || seen[target.ID] {
			return nil, ErrInvalid
		}
		seen[target.ID] = true
	}

	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return nil, err
	}
	_, _, err = s.membership(ctx, tx, archiveID, actor, "shred")
	_ = tx.Rollback()
	if err != nil {
		return nil, err
	}

	results := make([]TrashShredEvent, 0, len(targets))
	for _, target := range targets {
		status, err := s.shred(ctx, actor, archiveID, target.ID, target.Name, true, &target)
		if err != nil {
			status = ShredStatus{Status: "blocked"}
			switch {
			case errors.Is(err, ErrForbidden):
				status.Blocker = "permission_changed"
			case errors.Is(err, ErrNotFound), errors.Is(err, ErrConflict):
				status.Blocker = "document_changed"
			default:
				slog.ErrorContext(ctx, "trash shred authorization", "document_id", target.ID, "error", err)
				status.Blocker = "authorization_error"
			}
		}
		results = append(results, TrashShredEvent{target.ID, status})
	}
	return results, nil
}

func (s *Service) previewTrashShredHTTP(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	preview, err := s.previewTrashShred(r.Context(), actor, r.PathValue("archiveId"), r.URL.Query().Get("documentId"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, requestID, preview)
}

func (s *Service) authorizeTrashShredHTTP(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var body struct {
		ConfirmedCount int                `json:"confirmedCount"`
		Targets        []TrashShredTarget `json:"targets"`
	}
	if err := decodeJSONLimited(w, r, &body, 8<<20); err != nil || body.ConfirmedCount != len(body.Targets) {
		writeFailure(w, ErrInvalid, requestID)
		return
	}
	results, err := s.authorizeTrashShred(r.Context(), actor, r.PathValue("archiveId"), body.Targets)
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, requestID, map[string]any{"items": results})
}
