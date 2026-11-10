package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
	"time"
)

type Sender interface {
	Send(context.Context, string, string, string) error
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
	if _, err := mail.ParseAddress(s.From); err != nil {
		return err
	}
	msg := codeMessage(language, code)
	var data bytes.Buffer
	fmt.Fprintf(&data, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", s.From, recipient, mime.QEncoding.Encode("utf-8", msg.subject), msg.body)
	result := make(chan error, 1)
	go func() { result <- smtp.SendMail(s.Addr, nil, s.From, []string{recipient}, data.Bytes()) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type SendGridSender struct {
	APIKey, From string
	Client       *http.Client
}

func (s SendGridSender) Send(ctx context.Context, recipient, language, code string) error {
	msg := codeMessage(language, code)
	payload := map[string]any{
		"personalizations": []any{map[string]any{"to": []any{map[string]string{"email": recipient}}}},
		"from":             map[string]string{"email": s.From},
		"subject":          msg.subject,
		"content":          []any{map[string]string{"type": "text/plain", "value": msg.body}},
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
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := s.deliverOne(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
			log.Printf("auth delivery retry scheduled: %T", err)
		}
	}
}

func (s *Service) deliverOne(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM invoyis.auth_delivery_outbox o USING invoyis.auth_challenges c
		WHERE o.challenge_id=c.id AND (c.expires_at <= $1 OR c.invalidated_at IS NOT NULL OR c.consumed_at IS NOT NULL)`, s.Now().UTC()); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var challengeID, email, language string
	var encrypted []byte
	var attempts int
	err = tx.QueryRowContext(ctx, `SELECT o.challenge_id,c.email,c.locale,o.encrypted_code,o.attempts
		FROM invoyis.auth_delivery_outbox o JOIN invoyis.auth_challenges c ON c.id=o.challenge_id
		WHERE o.next_attempt_at <= $1 AND c.expires_at > $1 AND c.invalidated_at IS NULL AND c.consumed_at IS NULL
		ORDER BY o.next_attempt_at LIMIT 1 FOR UPDATE OF o SKIP LOCKED`, s.Now().UTC()).Scan(&challengeID, &email, &language, &encrypted, &attempts)
	if err != nil {
		return err
	}
	code, err := s.decryptCode(encrypted)
	if err != nil {
		return err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = s.Sender.Send(sendCtx, email, language, code)
	if err == nil {
		_, err = tx.ExecContext(ctx, `DELETE FROM invoyis.auth_delivery_outbox WHERE challenge_id=$1`, challengeID)
	} else {
		backoff := time.Duration(1<<min(attempts, 6)) * time.Second
		_, updateErr := tx.ExecContext(ctx, `UPDATE invoyis.auth_delivery_outbox SET attempts=attempts+1,next_attempt_at=$2 WHERE challenge_id=$1`, challengeID, s.Now().UTC().Add(backoff))
		if updateErr != nil {
			return updateErr
		}
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return commitErr
	}
	return err
}
