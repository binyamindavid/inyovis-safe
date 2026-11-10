package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"net/url"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	Purpose      = "sign_in"
	CookieName   = "__Host-invoyis_session"
	challengeTTL = 10 * time.Minute
	sessionTTL   = 24 * time.Hour
	rateWindow   = 15 * time.Minute
)

type Service struct {
	DB            *sql.DB
	Key           []byte
	GatewaySecret string
	BrowserOrigin string
	Sender        Sender
	Now           func() time.Time
}

func New(databaseURL, keyHex, gatewaySecret, browserOrigin string, sender Sender) (*Service, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return nil, errors.New("AUTH_SECRET must be 32 random bytes encoded as hex")
	}
	if len(gatewaySecret) < 32 || browserOrigin == "" || sender == nil || databaseURL == "" {
		return nil, errors.New("auth database, gateway secret, browser origin, and sender are required")
	}
	origin, err := url.Parse(browserOrigin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, errors.New("BROWSER_ORIGIN must be an HTTPS origin without a path")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	return &Service{DB: db, Key: key, GatewaySecret: gatewaySecret, BrowserOrigin: browserOrigin, Sender: sender, Now: time.Now}, nil
}

func (s *Service) Close() error { return s.DB.Close() }

func NormalizeEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	if len(email) == 0 || len(email) > 254 || strings.ContainsAny(email, "\r\n\t ") {
		return "", errors.New("invalid email")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return "", errors.New("invalid email")
	}
	return email, nil
}

func locale(value string) string {
	for _, language := range strings.Split(value, ",") {
		code := strings.ToLower(strings.TrimSpace(strings.SplitN(language, ";", 2)[0]))
		if strings.HasPrefix(code, "fr") {
			return "fr"
		}
		if strings.HasPrefix(code, "he") {
			return "he"
		}
		if strings.HasPrefix(code, "en") {
			return "en"
		}
	}
	return "en"
}

func randomBytes(size int) ([]byte, error) {
	value := make([]byte, size)
	_, err := rand.Read(value)
	return value, err
}

func randomCode() (string, error) {
	number, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", number.Int64()), nil
}

func randomID() (string, error) {
	bytes, err := randomBytes(16)
	if err != nil {
		return "", err
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:]), nil
}

func (s *Service) digest(parts ...string) []byte {
	mac := hmac.New(sha256.New, s.Key)
	for _, part := range parts {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return mac.Sum(nil)
}

func (s *Service) encryptCode(code string) ([]byte, error) {
	key := s.digest("outbox-key")
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, err := randomBytes(aead.NonceSize())
	if err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(code), nil), nil
}

func (s *Service) decryptCode(encrypted []byte) (string, error) {
	block, err := aes.NewCipher(s.digest("outbox-key"))
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(encrypted) < aead.NonceSize() {
		return "", errors.New("invalid encrypted code")
	}
	plain, err := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *Service) rate(ctx context.Context, tx *sql.Tx, kind, key string, max int, window time.Duration, now time.Time) (bool, error) {
	var hits int
	err := tx.QueryRowContext(ctx, `INSERT INTO invoyis.auth_rate_limits (kind,key_digest,window_start,hits)
		VALUES ($1,$2,$3,1) ON CONFLICT (kind,key_digest) DO UPDATE SET
		hits=CASE WHEN invoyis.auth_rate_limits.window_start < $4 THEN 1 ELSE invoyis.auth_rate_limits.hits+1 END,
		window_start=CASE WHEN invoyis.auth_rate_limits.window_start < $4 THEN $3 ELSE invoyis.auth_rate_limits.window_start END
		RETURNING hits`, kind, s.digest("rate", kind, key), now, now.Add(-window)).Scan(&hits)
	return hits > max, err
}

func (s *Service) RequestCode(ctx context.Context, rawEmail, ip, language string) (bool, error) {
	return s.RequestCodeForInvitation(ctx, rawEmail, ip, language, "")
}

func (s *Service) RequestCodeForInvitation(ctx context.Context, rawEmail, ip, language, invitationToken string) (bool, error) {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return false, err
	}
	now := s.Now().UTC()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	for _, item := range []struct {
		kind, key string
		max       int
	}{{"request-email", email, 5}, {"request-ip", ip, 30}} {
		limited, err := s.rate(ctx, tx, item.kind, item.key, item.max, rateWindow, now)
		if err != nil {
			return false, err
		}
		if limited {
			if err := tx.Commit(); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	var accountID sql.NullInt64
	var eligible bool
	var pendingInvitationID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id,eligible,pending_invitation_id FROM invoyis.accounts WHERE email=$1`, email).Scan(&accountID, &eligible, &pendingInvitationID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var invitationID sql.NullString
	if !eligible {
		if len(invitationToken) == 43 {
			tokenDigest := sha256.Sum256([]byte(invitationToken))
			err := tx.QueryRowContext(ctx, `SELECT id FROM invoyis.archive_invitations
				WHERE token_digest=$1 AND email=$2 AND revoked_at IS NULL AND consumed_at IS NULL AND expires_at>$3`, tokenDigest[:], email, now).Scan(&invitationID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return false, err
			}
		}
		if accountID.Valid && !pendingInvitationID.Valid {
			invitationID = sql.NullString{}
		}
		if !invitationID.Valid {
			accountID = sql.NullInt64{}
		}
	}
	var previousID string
	var previousDigest []byte
	err = tx.QueryRowContext(ctx, `SELECT id,code_digest FROM invoyis.auth_challenges WHERE email=$1 AND purpose='sign_in' ORDER BY created_at DESC,id DESC LIMIT 1`, email).Scan(&previousID, &previousDigest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE invoyis.auth_challenges SET invalidated_at=$2 WHERE email=$1 AND purpose='sign_in' AND consumed_at IS NULL AND invalidated_at IS NULL`, email, now); err != nil {
		return false, err
	}
	var code string
	for {
		code, err = randomCode()
		if err != nil {
			return false, err
		}
		if previousID == "" || subtle.ConstantTimeCompare(previousDigest, s.digest("code", previousID, email, Purpose, code)) != 1 {
			break
		}
	}
	id, err := randomID()
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO invoyis.auth_challenges (id,email,account_id,purpose,code_digest,locale,expires_at,created_at,invitation_id)
		VALUES ($1,$2,$3,'sign_in',$4,$5,$6,$7,$8)`, id, email, accountID, s.digest("code", id, email, Purpose, code), locale(language), now.Add(challengeTTL), now, invitationID); err != nil {
		return false, err
	}
	if eligible || invitationID.Valid {
		encrypted, err := s.encryptCode(code)
		if err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO invoyis.auth_delivery_outbox (challenge_id,encrypted_code,next_attempt_at) VALUES ($1,$2,$3)`, id, encrypted, now); err != nil {
			return false, err
		}
	}
	return false, tx.Commit()
}

type Verification struct {
	Token     string
	Email     string
	ExpiresAt time.Time
}

var ErrInvalidCode = errors.New("invalid code")
var ErrExpiredCode = errors.New("expired code")

func (s *Service) VerifyCode(ctx context.Context, rawEmail, code, ip string) (Verification, bool, error) {
	return s.VerifyCodeForInvitation(ctx, rawEmail, code, ip, "")
}

func (s *Service) VerifyCodeForInvitation(ctx context.Context, rawEmail, code, ip, invitationToken string) (Verification, bool, error) {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return Verification{}, false, err
	}
	now := s.Now().UTC()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Verification{}, false, err
	}
	defer tx.Rollback()
	finishInvalid := func(reason error) (Verification, bool, error) {
		if err := tx.Commit(); err != nil {
			return Verification{}, false, err
		}
		return Verification{}, false, reason
	}
	for _, item := range []struct {
		kind, key string
		max       int
		window    time.Duration
	}{{"verify-email", email, 10, rateWindow}, {"verify-ip", ip, 60, rateWindow}, {"verify-email-day", email, 50, 24 * time.Hour}} {
		limited, err := s.rate(ctx, tx, item.kind, item.key, item.max, item.window, now)
		if err != nil {
			return Verification{}, false, err
		}
		if limited {
			if err := tx.Commit(); err != nil {
				return Verification{}, false, err
			}
			return Verification{}, true, nil
		}
	}
	var id string
	var accountID sql.NullInt64
	var invitationID sql.NullString
	var digest []byte
	var expiresAt time.Time
	var invalidatedAt, consumedAt sql.NullTime
	var attempts int
	err = tx.QueryRowContext(ctx, `SELECT id,account_id,code_digest,expires_at,invalidated_at,consumed_at,verify_attempts,invitation_id
		FROM invoyis.auth_challenges WHERE email=$1 AND purpose='sign_in'
		ORDER BY created_at DESC,id DESC LIMIT 1 FOR UPDATE`, email).Scan(&id, &accountID, &digest, &expiresAt, &invalidatedAt, &consumedAt, &attempts, &invitationID)
	if errors.Is(err, sql.ErrNoRows) {
		return finishInvalid(ErrInvalidCode)
	}
	if err != nil {
		return Verification{}, false, err
	}
	if invalidatedAt.Valid || consumedAt.Valid || attempts >= 5 {
		return finishInvalid(ErrInvalidCode)
	}
	if !now.Before(expiresAt) {
		return finishInvalid(ErrExpiredCode)
	}
	var accountEligible bool
	var pendingInvitationID sql.NullString
	if accountID.Valid {
		if err := tx.QueryRowContext(ctx, `SELECT eligible,pending_invitation_id FROM invoyis.accounts WHERE id=$1 FOR UPDATE`, accountID.Int64).Scan(&accountEligible, &pendingInvitationID); errors.Is(err, sql.ErrNoRows) {
			return finishInvalid(ErrInvalidCode)
		} else if err != nil {
			return Verification{}, false, err
		}
		if !accountEligible && !pendingInvitationID.Valid {
			return finishInvalid(ErrInvalidCode)
		}
	}
	validDigits := len(code) == 6
	for _, character := range code {
		validDigits = validDigits && character >= '0' && character <= '9'
	}
	if !validDigits || subtle.ConstantTimeCompare(digest, s.digest("code", id, email, Purpose, code)) != 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE invoyis.auth_challenges SET verify_attempts=verify_attempts+1 WHERE id=$1`, id); err != nil {
			return Verification{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return Verification{}, false, err
		}
		return Verification{}, false, ErrInvalidCode
	}
	if !accountEligible {
		if !invitationID.Valid || len(invitationToken) != 43 {
			return finishInvalid(ErrInvalidCode)
		}
		tokenDigest := sha256.Sum256([]byte(invitationToken))
		var activeInvitationID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM invoyis.archive_invitations
			WHERE id=$1 AND token_digest=$2 AND email=$3 AND revoked_at IS NULL
			AND consumed_at IS NULL AND expires_at>$4 FOR SHARE`, invitationID.String, tokenDigest[:], email, now).Scan(&activeInvitationID)
		if errors.Is(err, sql.ErrNoRows) {
			return finishInvalid(ErrInvalidCode)
		}
		if err != nil {
			return Verification{}, false, err
		}
		if accountID.Valid {
			if _, err := tx.ExecContext(ctx, `UPDATE invoyis.accounts SET pending_invitation_id=$2
				WHERE id=$1 AND eligible=false AND pending_invitation_id IS NOT NULL`, accountID.Int64, activeInvitationID); err != nil {
				return Verification{}, false, err
			}
		} else {
			err = tx.QueryRowContext(ctx, `INSERT INTO invoyis.accounts (email,eligible,pending_invitation_id)
				VALUES ($1,false,$2) ON CONFLICT (email) DO NOTHING RETURNING id`, email, activeInvitationID).Scan(&accountID.Int64)
			if errors.Is(err, sql.ErrNoRows) {
				return finishInvalid(ErrInvalidCode)
			}
			if err != nil {
				return Verification{}, false, err
			}
			accountID.Valid = true
		}
	}
	random, err := randomBytes(32)
	if err != nil {
		return Verification{}, false, err
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	tokenDigest := sha256.Sum256([]byte(token))
	expiry := now.Add(sessionTTL)
	if _, err := tx.ExecContext(ctx, `INSERT INTO invoyis.sessions (account_id,token_digest,expires_at,created_at) VALUES ($1,$2,$3,$4)`, accountID.Int64, tokenDigest[:], expiry, now); err != nil {
		return Verification{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invoyis.auth_challenges SET consumed_at=$2 WHERE id=$1`, id, now); err != nil {
		return Verification{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Verification{}, false, err
	}
	return Verification{Token: token, Email: email, ExpiresAt: expiry}, false, nil
}

func (s *Service) SessionAccount(ctx context.Context, token string) (int64, string, time.Time, error) {
	accountID, email, expiry, eligible, err := s.SessionIdentity(ctx, token)
	if err == nil && !eligible {
		return 0, "", time.Time{}, sql.ErrNoRows
	}
	return accountID, email, expiry, err
}

func (s *Service) SessionIdentity(ctx context.Context, token string) (int64, string, time.Time, bool, error) {
	if token == "" {
		return 0, "", time.Time{}, false, sql.ErrNoRows
	}
	digest := sha256.Sum256([]byte(token))
	var accountID int64
	var email string
	var expiry time.Time
	var eligible bool
	err := s.DB.QueryRowContext(ctx, `SELECT a.id,a.email,s.expires_at,a.eligible FROM invoyis.sessions s JOIN invoyis.accounts a ON a.id=s.account_id
		WHERE s.token_digest=$1 AND s.revoked_at IS NULL AND s.expires_at>$2`, digest[:], s.Now().UTC()).Scan(&accountID, &email, &expiry, &eligible)
	return accountID, email, expiry, eligible, err
}

func (s *Service) Session(ctx context.Context, token string) (string, time.Time, error) {
	_, email, expiry, err := s.SessionAccount(ctx, token)
	return email, expiry, err
}

func (s *Service) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	digest := sha256.Sum256([]byte(token))
	_, err := s.DB.ExecContext(ctx, `UPDATE invoyis.sessions SET revoked_at=$2 WHERE token_digest=$1 AND revoked_at IS NULL`, digest[:], s.Now().UTC())
	return err
}
