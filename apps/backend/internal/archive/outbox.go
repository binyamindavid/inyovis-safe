package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"
)

type invitationDelivery struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type queuedInvitation struct {
	id, claim string
	encrypted []byte
	attempts  int
	expires   time.Time
}

const invitationBatchSize = 16
const invitationConcurrency = 8
const invitationLease = 90 * time.Second

func (s *Service) RunInvitationOutbox(ctx context.Context) {
	ticker := time.NewTicker(300 * time.Millisecond)
	cleanup := time.NewTicker(time.Hour)
	defer ticker.Stop()
	defer cleanup.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		count, err := s.deliverInvitationBatch(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("invitation delivery failed phase=%s", err.Error())
		}
		if count > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-cleanup.C:
			if _, err := s.Auth.DB.ExecContext(ctx, `DELETE FROM invoyis.archive_invitation_outbox o USING invoyis.archive_invitations i
				WHERE o.invitation_id=i.id AND (i.expires_at<now() OR i.revoked_at IS NOT NULL OR i.consumed_at IS NOT NULL)`); err != nil && ctx.Err() == nil {
				log.Printf("invitation cleanup failed phase=database")
			}
		}
	}
}

func (s *Service) claimInvitations(ctx context.Context) ([]queuedInvitation, error) {
	claim, err := newID()
	if err != nil {
		return nil, err
	}
	tx, err := s.Auth.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `WITH ready AS (
		SELECT o.invitation_id,i.expires_at FROM invoyis.archive_invitation_outbox o
		JOIN invoyis.archive_invitations i ON i.id=o.invitation_id
		WHERE o.next_attempt_at<=now() AND (o.locked_until IS NULL OR o.locked_until<=now())
		AND i.expires_at>now() AND i.revoked_at IS NULL AND i.consumed_at IS NULL
		ORDER BY o.next_attempt_at,o.invitation_id LIMIT $1 FOR UPDATE OF o SKIP LOCKED
	) UPDATE invoyis.archive_invitation_outbox o SET locked_until=now()+$2::interval,claim_token=$3
	FROM ready WHERE o.invitation_id=ready.invitation_id
	RETURNING o.invitation_id,o.encrypted_message,o.attempts,ready.expires_at`, invitationBatchSize, invitationLease.String(), claim)
	if err != nil {
		return nil, err
	}
	var batch []queuedInvitation
	for rows.Next() {
		var item queuedInvitation
		if err := rows.Scan(&item.id, &item.encrypted, &item.attempts, &item.expires); err != nil {
			rows.Close()
			return nil, err
		}
		item.claim = claim
		batch = append(batch, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return batch, nil
}

func (s *Service) acknowledgeInvitation(item queuedInvitation, sent bool) error {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var result sql.Result
		if sent {
			result, last = s.Auth.DB.ExecContext(ctx, `DELETE FROM invoyis.archive_invitation_outbox WHERE invitation_id=$1 AND claim_token=$2`, item.id, item.claim)
		} else {
			backoff := time.Duration(1<<min(item.attempts, 6)) * time.Second
			result, last = s.Auth.DB.ExecContext(ctx, `UPDATE invoyis.archive_invitation_outbox SET attempts=attempts+1,
				next_attempt_at=now()+$3::interval,locked_until=NULL,claim_token=NULL
				WHERE invitation_id=$1 AND claim_token=$2`, item.id, item.claim, backoff.String())
		}
		cancel()
		if last == nil {
			var affected int64
			affected, last = result.RowsAffected()
			if last == nil && affected == 0 {
				last = errors.New("claim lost")
			}
		}
		if last == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	return last
}

func (s *Service) deliverInvitation(ctx context.Context, item queuedInvitation) error {
	plain, err := s.Auth.OpenInvitationMessage(item.encrypted)
	if err != nil {
		if ack := s.acknowledgeInvitation(item, true); ack != nil {
			return errors.New("ack")
		}
		return errors.New("decrypt")
	}
	var message invitationDelivery
	if json.Unmarshal(plain, &message) != nil || message.To == "" || message.Subject == "" || message.Body == "" {
		if ack := s.acknowledgeInvitation(item, true); ack != nil {
			return errors.New("ack")
		}
		return errors.New("payload")
	}
	if !s.Now().UTC().Before(item.expires) {
		return s.acknowledgeInvitation(item, true)
	}
	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = s.Sender.SendText(sendCtx, message.To, message.Subject, message.Body)
	cancel()
	if ack := s.acknowledgeInvitation(item, err == nil); ack != nil {
		return errors.New("ack")
	}
	if err != nil {
		return errors.New("send")
	}
	return nil
}

func (s *Service) deliverInvitationBatch(ctx context.Context) (int, error) {
	batch, err := s.claimInvitations(ctx)
	if err != nil {
		return 0, errors.New("claim")
	}
	sem := make(chan struct{}, invitationConcurrency)
	results := make(chan error, len(batch))
	var wait sync.WaitGroup
	for _, item := range batch {
		sem <- struct{}{}
		wait.Add(1)
		go func(item queuedInvitation) {
			defer wait.Done()
			defer func() { <-sem }()
			if err := s.deliverInvitation(ctx, item); err != nil {
				results <- err
			}
		}(item)
	}
	wait.Wait()
	close(results)
	var first error
	for err := range results {
		if first == nil {
			first = err
		}
	}
	return len(batch), first
}
