package archive

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"net/http"
	"strings"
)

const forwardingDomain = "inbound.invoyis.com"

type ForwardingAddress struct {
	Enabled bool   `json:"enabled"`
	Address string `json:"address,omitempty"`
}

func forwardingToken() (string, error) {
	var entropy [10]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(entropy[:])), nil
}

func forwardingAddress(token string, enabled bool) ForwardingAddress {
	return ForwardingAddress{Enabled: enabled, Address: token + "@" + forwardingDomain}
}

func (s *Service) Forwarding(ctx context.Context, actor int64, archiveID string) (ForwardingAddress, error) {
	if !uuidPattern.MatchString(archiveID) {
		return ForwardingAddress{}, ErrNotFound
	}
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return ForwardingAddress{}, err
	}
	defer tx.Rollback()
	if _, _, err = s.membership(ctx, tx, archiveID, actor, "settings"); err != nil {
		return ForwardingAddress{}, err
	}
	var token, state string
	err = tx.QueryRowContext(ctx, `SELECT token,state FROM invoyis.forwarding_addresses WHERE archive_id=$1 AND state<>'retired'`, archiveID).Scan(&token, &state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ForwardingAddress{}, err
	}
	if err = tx.Commit(); err != nil {
		return ForwardingAddress{}, err
	}
	if token == "" {
		return ForwardingAddress{}, nil
	}
	return forwardingAddress(token, state == "active"), nil
}

// The archive row serializes concurrent enables and rotations, including the first enable.
func (s *Service) ChangeForwarding(ctx context.Context, actor int64, archiveID, action string) (ForwardingAddress, error) {
	if !uuidPattern.MatchString(archiveID) {
		return ForwardingAddress{}, ErrNotFound
	}
	if action != "enable" && action != "disable" && action != "rotate" {
		return ForwardingAddress{}, ErrInvalid
	}
	if action != "disable" && (s.InboundPublicKey == nil || s.Storage == nil) {
		return ForwardingAddress{}, ErrStorageUnavailable
	}
	tx, err := s.transaction(ctx, actor, archiveID)
	if err != nil {
		return ForwardingAddress{}, err
	}
	defer tx.Rollback()
	if _, _, err = s.membership(ctx, tx, archiveID, actor, "settings"); err != nil {
		return ForwardingAddress{}, err
	}
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM invoyis.archives WHERE id=$1 FOR UPDATE`, archiveID).Scan(&id); err != nil {
		return ForwardingAddress{}, err
	}
	var token, state string
	err = tx.QueryRowContext(ctx, `SELECT token,state FROM invoyis.forwarding_addresses WHERE archive_id=$1 AND state<>'retired' FOR UPDATE`, archiveID).Scan(&token, &state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ForwardingAddress{}, err
	}
	if action == "disable" {
		if token != "" {
			if _, err = tx.ExecContext(ctx, `UPDATE invoyis.forwarding_addresses SET state='disabled' WHERE token=$1`, token); err != nil {
				return ForwardingAddress{}, err
			}
		}
	} else if action == "enable" && token != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE invoyis.forwarding_addresses SET state='active' WHERE token=$1`, token); err != nil {
			return ForwardingAddress{}, err
		}
	} else {
		if token != "" {
			if _, err = tx.ExecContext(ctx, `UPDATE invoyis.forwarding_addresses SET state='retired',retired_at=now() WHERE token=$1`, token); err != nil {
				return ForwardingAddress{}, err
			}
		}
		token = ""
		for attempt := 0; attempt < 8; attempt++ {
			candidate, e := forwardingToken()
			if e != nil {
				return ForwardingAddress{}, e
			}
			var inserted string
			e = tx.QueryRowContext(ctx, `INSERT INTO invoyis.forwarding_addresses(token,archive_id,state) VALUES($1,$2,'active') ON CONFLICT DO NOTHING RETURNING token`, candidate, archiveID).Scan(&inserted)
			if e == nil {
				token = inserted
				break
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return ForwardingAddress{}, e
			}
		}
		if token == "" {
			return ForwardingAddress{}, ErrConflict
		}
	}
	if err = s.audit(ctx, tx, archiveID, actor, "forwarding_"+action, nil, map[string]any{}); err != nil {
		return ForwardingAddress{}, err
	}
	if err = tx.Commit(); err != nil {
		return ForwardingAddress{}, err
	}
	if token == "" {
		return ForwardingAddress{}, nil
	}
	return forwardingAddress(token, action != "disable"), nil
}

func (s *Service) forwardingHTTP(w http.ResponseWriter, r *http.Request, requestID, _ string) {
	actor, _, ok := s.account(w, r, requestID)
	if !ok {
		return
	}
	var value ForwardingAddress
	var err error
	if r.Method == http.MethodGet {
		value, err = s.Forwarding(r.Context(), actor, r.PathValue("archiveId"))
	} else {
		value, err = s.ChangeForwarding(r.Context(), actor, r.PathValue("archiveId"), r.PathValue("action"))
	}
	if err != nil {
		writeFailure(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, requestID, value)
}
