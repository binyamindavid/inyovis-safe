package archive

import (
	"context"
	"net/http"
	"time"
)

type InboundOutcome struct {
	ID            string `json:"id"`
	Outcome       string `json:"outcome"`
	AcceptedCount int    `json:"acceptedCount"`
	RejectedCount int    `json:"rejectedCount"`
	ReceivedAt    string `json:"receivedAt"`
}

func (s *Service) InboundOutcomes(ctx context.Context, actor int64, archiveID string) ([]InboundOutcome, error) {
	if !uuidPattern.MatchString(archiveID) {
		return nil, ErrNotFound
	}
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, _, err = s.membership(ctx, tx, archiveID, actor, "view"); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT fingerprint,outcome,accepted_count,rejected_count,received_at
 FROM invoyis.inbound_deliveries WHERE archive_id=$1 ORDER BY received_at DESC,fingerprint DESC LIMIT 30`, archiveID)
	if err != nil {
		return nil, err
	}
	items := []InboundOutcome{}
	for rows.Next() {
		var item InboundOutcome
		var received time.Time
		if err = rows.Scan(&item.ID, &item.Outcome, &item.AcceptedCount, &item.RejectedCount, &received); err != nil {
			rows.Close()
			return nil, err
		}
		item.ReceivedAt = received.UTC().Format(time.RFC3339)
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return items, tx.Commit()
}

func (s *Service) inboundOutcomesHTTP(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	items, err := s.InboundOutcomes(r.Context(), actor, r.PathValue("archiveId"))
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, requestID, map[string]any{"items": items})
}
