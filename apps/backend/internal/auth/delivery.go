package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
	"sync"
	"time"
)

type Sender interface {
	Send(context.Context, string, string, string) error
	SendText(context.Context, string, string, string) error
}

type message struct{ subject, body string }

func codeMessage(language, code string) message {
	switch language {
	case "fr":
		return message{"Votre code Invoyis", "Votre code de connexion Invoyis est " + code + ". Il expire dans 10 minutes."}
	case "he":
		return message{"קוד הכניסה ל-Invoyis", "קוד הכניסה ל-Invoyis הוא " + code + ". תוקפו יפוג בעוד 10 דקות."}
	default:
		return message{"Your Invoyis code", "Your Invoyis sign-in code is " + code + ". It expires in 10 minutes."}
	}
}

type SMTPSender struct{ Addr, From string }

func (s SMTPSender) Send(ctx context.Context, recipient, language, code string) error {
	msg := codeMessage(language, code)
	return s.SendText(ctx, recipient, msg.subject, msg.body)
}

func (s SMTPSender) SendText(ctx context.Context, recipient, subject, body string) error {
	if _, err := mail.ParseAddress(s.From); err != nil {
		return err
	}
	var data bytes.Buffer
	fmt.Fprintf(&data, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", s.From, recipient, mime.QEncoding.Encode("utf-8", subject), body)
	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return err
	}
	connection, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return err
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	client, err := smtp.NewClient(connection, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Mail(s.From); err != nil {
		return err
	}
	if err := client.Rcpt(recipient); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(data.Bytes()); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	// DATA was accepted. A failed QUIT cannot safely turn this into a retry.
	_ = client.Quit()
	return nil
}

type SendGridSender struct {
	APIKey, From string
	Client       *http.Client
}

func (s SendGridSender) Send(ctx context.Context, recipient, language, code string) error {
	msg := codeMessage(language, code)
	return s.SendText(ctx, recipient, msg.subject, msg.body)
}

func (s SendGridSender) SendText(ctx context.Context, recipient, subject, body string) error {
	payload := map[string]any{
		"personalizations": []any{map[string]any{"to": []any{map[string]string{"email": recipient}}}},
		"from":             map[string]string{"email": s.From},
		"subject":          subject,
		"content":          []any{map[string]string{"type": "text/plain", "value": body}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.sendgrid.com/v3/mail/send", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusAccepted {
		return errors.New("email provider rejected delivery")
	}
	return nil
}

func SenderFromEnv() (Sender, error) {
	from := os.Getenv("MAIL_FROM")
	parsedFrom, err := mail.ParseAddress(from)
	if err != nil || parsedFrom.Address != from || strings.ContainsAny(from, "\r\n") {
		return nil, errors.New("MAIL_FROM must be a valid address")
	}
	switch os.Getenv("MAIL_DRIVER") {
	case "smtp":
		addr := os.Getenv("SMTP_ADDR")
		if addr == "" {
			return nil, errors.New("SMTP_ADDR is required")
		}
		return SMTPSender{Addr: addr, From: from}, nil
	case "sendgrid":
		key := os.Getenv("SENDGRID_API_KEY")
		if key == "" {
			return nil, errors.New("SENDGRID_API_KEY is required")
		}
		return SendGridSender{APIKey: key, From: from}, nil
	default:
		return nil, errors.New("MAIL_DRIVER must be smtp or sendgrid")
	}
}

func (s *Service) RunOutbox(ctx context.Context) {
	ticker := time.NewTicker(300 * time.Millisecond)
	cleanupTicker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	defer cleanupTicker.Stop()
	for {
		count, err := s.deliverBatch(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("auth delivery failed phase=%s", deliveryPhase(err))
		}
		if count > 0 {
			select {
			case <-cleanupTicker.C:
				if err := s.cleanupAuth(ctx); err != nil && ctx.Err() == nil {
					log.Printf("auth cleanup failed phase=database")
				}
			default:
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-cleanupTicker.C:
			if err := s.cleanupAuth(ctx); err != nil && ctx.Err() == nil {
				log.Printf("auth cleanup failed phase=database")
			}
		}
	}
}

type deliveryError struct {
	phase string
	err   error
}

func (e *deliveryError) Error() string { return e.phase }
func (e *deliveryError) Unwrap() error { return e.err }

func deliveryPhase(err error) string {
	var delivery *deliveryError
	if errors.As(err, &delivery) {
		if delivery.phase == "send" && errors.Is(err, context.DeadlineExceeded) {
			return "send_timeout"
		}
		return delivery.phase
	}
	return "database"
}

type queuedDelivery struct {
	id, email, language, claimToken string
	encrypted                       []byte
	attempts                        int
	expiresAt                       time.Time
}

const (
	deliveryBatchSize   = 16
	deliveryConcurrency = 8
	deliverySendTimeout = 5 * time.Second
	deliveryAckTimeout  = 2 * time.Second
	deliveryAckAttempts = 3
	// Two send rounds plus three acknowledgement attempts per round, retry pauses,
	// and 30 seconds for scheduler and database contention.
	deliveryClaimLease = time.Duration((deliveryBatchSize+deliveryConcurrency-1)/deliveryConcurrency)*
		(deliverySendTimeout+deliveryAckAttempts*deliveryAckTimeout+300*time.Millisecond) + 30*time.Second
)

func (s *Service) claimBatch(ctx context.Context) ([]queuedDelivery, error) {
	now := s.Now().UTC()
	claimToken, err := randomID()
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `WITH ready AS (
		SELECT o.challenge_id,c.email,c.locale,c.expires_at
		FROM invoyis.auth_delivery_outbox o JOIN invoyis.auth_challenges c ON c.id=o.challenge_id
		WHERE o.next_attempt_at <= $1 AND (o.locked_until IS NULL OR o.locked_until <= $1)
			AND c.expires_at > $1 AND c.invalidated_at IS NULL AND c.consumed_at IS NULL
		ORDER BY o.next_attempt_at,o.challenge_id LIMIT $4 FOR UPDATE OF o SKIP LOCKED
	) UPDATE invoyis.auth_delivery_outbox o
	SET locked_until=$2,claim_token=$3 FROM ready WHERE o.challenge_id=ready.challenge_id
	RETURNING o.challenge_id,ready.email,ready.locale,o.encrypted_code,o.attempts,ready.expires_at`, now, now.Add(deliveryClaimLease), claimToken, deliveryBatchSize)
	if err != nil {
		return nil, err
	}
	var batch []queuedDelivery
	for rows.Next() {
		var item queuedDelivery
		if err := rows.Scan(&item.id, &item.email, &item.language, &item.encrypted, &item.attempts, &item.expiresAt); err != nil {
			rows.Close()
			return nil, err
		}
		item.claimToken = claimToken
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

func (s *Service) deliverClaimed(ctx context.Context, item queuedDelivery) error {
	code, err := s.decryptCode(item.encrypted)
	if err != nil {
		if ackErr := s.ackDelivery(item, nil); ackErr != nil {
			return &deliveryError{"ack", ackErr}
		}
		return &deliveryError{"decrypt", err}
	}
	if !s.Now().UTC().Before(item.expiresAt) {
		return s.ackDelivery(item, nil)
	}
	sendCtx, cancel := context.WithTimeout(ctx, deliverySendTimeout)
	err = s.Sender.Send(sendCtx, item.email, item.language, code)
	cancel()
	ackErr := s.ackDelivery(item, err)
	if ackErr != nil {
		return &deliveryError{"ack", ackErr}
	}
	if err != nil {
		return &deliveryError{"send", err}
	}
	return nil
}

func (s *Service) ackDelivery(item queuedDelivery, sendErr error) error {
	// Acknowledge after cancellation too, so shutdown does not immediately redeliver a successful send.
	var lastErr error
	for attempt := range deliveryAckAttempts {
		ctx, cancel := context.WithTimeout(context.Background(), deliveryAckTimeout)
		var result interface{ RowsAffected() (int64, error) }
		if sendErr == nil {
			result, lastErr = s.DB.ExecContext(ctx, `DELETE FROM invoyis.auth_delivery_outbox WHERE challenge_id=$1 AND claim_token=$2`, item.id, item.claimToken)
		} else {
			backoff := time.Duration(1<<min(item.attempts, 6)) * time.Second
			result, lastErr = s.DB.ExecContext(ctx, `UPDATE invoyis.auth_delivery_outbox
				SET attempts=attempts+1,next_attempt_at=$3,locked_until=NULL,claim_token=NULL
				WHERE challenge_id=$1 AND claim_token=$2`, item.id, item.claimToken, s.Now().UTC().Add(backoff))
		}
		cancel()
		if lastErr == nil {
			var affected int64
			affected, lastErr = result.RowsAffected()
			if lastErr == nil && affected == 0 {
				lastErr = errors.New("delivery claim no longer owned")
			}
		}
		if lastErr == nil {
			return nil
		}
		if attempt < deliveryAckAttempts-1 {
			time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
		}
	}
	return lastErr
}

func (s *Service) deliverBatch(ctx context.Context) (int, error) {
	batch, err := s.claimBatch(ctx)
	if err != nil {
		return 0, &deliveryError{"claim", err}
	}
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, deliveryConcurrency)
	errorsFound := make(chan error, len(batch))
	for _, item := range batch {
		semaphore <- struct{}{}
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer func() { <-semaphore }()
			if err := s.deliverClaimed(ctx, item); err != nil {
				errorsFound <- err
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		return len(batch), err
	}
	return len(batch), nil
}

func (s *Service) cleanupAuth(ctx context.Context) error {
	now := s.Now().UTC()
	for _, query := range []string{
		`DELETE FROM invoyis.auth_challenges WHERE created_at < $1 AND expires_at < $1`,
		`DELETE FROM invoyis.auth_rate_limits WHERE window_start < $1`,
		`DELETE FROM invoyis.sessions WHERE expires_at < $1 OR revoked_at < $1`,
		`DELETE FROM invoyis.accounts a WHERE a.eligible=false AND a.pending_invitation_id IS NOT NULL
			AND a.created_at < $1 AND EXISTS (SELECT 1 FROM invoyis.archive_invitations i
				WHERE i.id=a.pending_invitation_id AND (i.expires_at < now() OR i.revoked_at IS NOT NULL))
			AND NOT EXISTS (SELECT 1 FROM invoyis.sessions s WHERE s.account_id=a.id)
			AND NOT EXISTS (SELECT 1 FROM invoyis.auth_challenges c WHERE c.account_id=a.id)
			AND NOT EXISTS (SELECT 1 FROM invoyis.archive_memberships m WHERE m.account_id=a.id)`,
	} {
		if _, err := s.DB.ExecContext(ctx, query, now.Add(-7*24*time.Hour)); err != nil {
			return err
		}
	}
	return nil
}
