package archive

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/auth"
)

var ErrNotFound = errors.New("archive or membership not found")
var ErrForbidden = errors.New("archive permission denied")
var ErrConflict = errors.New("archive conflict")
var ErrInvalid = errors.New("invalid archive input")

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var jurisdictionPattern = regexp.MustCompile(`^[A-Z]{2}$`)

type Service struct {
	DB            *sql.DB
	Auth          *auth.Service
	Sender        auth.Sender
	BrowserOrigin string
	Now           func() time.Time
}

func New(databaseURL string, authService *auth.Service, sender auth.Sender, browserOrigin string) (*Service, error) {
	if databaseURL == "" || authService == nil || sender == nil || browserOrigin == "" {
		return nil, errors.New("ARCHIVE_DATABASE_URL, auth service, sender, and browser origin are required")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	var role string
	var superuser, bypassRLS, ownsArchives bool
	if err := db.QueryRow(`SELECT current_user,r.rolsuper,r.rolbypassrls,
		pg_get_userbyid(c.relowner)=current_user FROM pg_roles r
		JOIN pg_class c ON c.oid='invoyis.archives'::regclass WHERE r.rolname=current_user`).Scan(&role, &superuser, &bypassRLS, &ownsArchives); err != nil {
		db.Close()
		return nil, err
	}
	if superuser || bypassRLS || ownsArchives || role == "" {
		db.Close()
		return nil, errors.New("ARCHIVE_DATABASE_URL must use a restricted role without BYPASSRLS or table ownership")
	}
	return &Service{DB: db, Auth: authService, Sender: sender, BrowserOrigin: browserOrigin, Now: time.Now}, nil
}

func (s *Service) Close() error { return s.DB.Close() }

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:]), nil
}

func validRole(value string) bool {
	return value == "admin" || value == "contributor" || value == "viewer"
}

// Can is the permission map for future record endpoints as well as current archive routes.
func Can(role string, bulkExport bool, action string) bool {
	switch action {
	case "view", "search", "open":
		return validRole(role)
	case "submit", "correct":
		return role == "admin" || role == "contributor"
	case "settings", "members", "invite":
		return role == "admin"
	case "bulk_export":
		return validRole(role) && bulkExport
	default:
		return false
	}
}

func (s *Service) transaction(ctx context.Context, accountID int64, archiveID string) (*sql.Tx, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('invoyis.account_id',$1,true),set_config('invoyis.archive_id',$2,true)`, strconv.FormatInt(accountID, 10), archiveID); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

type Archive struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	Name          string  `json:"name"`
	Jurisdiction  *string `json:"jurisdiction"`
	Role          string  `json:"role"`
	CanBulkExport bool    `json:"canBulkExport"`
}

func scanArchive(row interface{ Scan(...any) error }) (Archive, error) {
	var item Archive
	var jurisdiction sql.NullString
	err := row.Scan(&item.ID, &item.Kind, &item.Name, &jurisdiction, &item.Role, &item.CanBulkExport)
	if jurisdiction.Valid {
		item.Jurisdiction = &jurisdiction.String
	}
	return item, err
}

func (s *Service) audit(ctx context.Context, tx *sql.Tx, archiveID string, actorID int64, action string, targetID *int64, details any) error {
	id, err := newID()
	if err != nil {
		return err
	}
	data, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO invoyis.archive_audit (id,archive_id,actor_id,action,target_account_id,details)
		VALUES ($1,$2,$3,$4,$5,$6)`, id, archiveID, actorID, action, targetID, data)
	return err
}

func (s *Service) membership(ctx context.Context, tx *sql.Tx, archiveID string, accountID int64, action string) (string, bool, error) {
	var role string
	var export bool
	err := tx.QueryRowContext(ctx, `SELECT role,can_bulk_export FROM invoyis.archive_memberships
		WHERE archive_id=$1 AND account_id=$2`, archiveID, accountID).Scan(&role, &export)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrNotFound
	}
	if err != nil {
		return "", false, err
	}
	if !Can(role, export, action) {
		return "", false, ErrForbidden
	}
	return role, export, nil
}

func (s *Service) Create(ctx context.Context, accountID int64, kind, name, jurisdiction string) (Archive, error) {
	name = strings.TrimSpace(name)
	jurisdiction = strings.ToUpper(strings.TrimSpace(jurisdiction))
	if (kind != "personal" && kind != "business") || len(name) < 1 || len(name) > 120 ||
		(kind == "business" && !jurisdictionPattern.MatchString(jurisdiction)) ||
		(jurisdiction != "" && !jurisdictionPattern.MatchString(jurisdiction)) {
		return Archive{}, ErrInvalid
	}
	id, err := newID()
	if err != nil {
		return Archive{}, err
	}
	tx, err := s.transaction(ctx, accountID, id)
	if err != nil {
		return Archive{}, err
	}
	defer tx.Rollback()
	var selected any
	if jurisdiction != "" {
		selected = jurisdiction
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO invoyis.archives (id,kind,name,jurisdiction,created_by)
		VALUES ($1,$2,$3,$4,$5)`, id, kind, name, selected, accountID); err != nil {
		return Archive{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO invoyis.archive_memberships (archive_id,account_id,role)
		VALUES ($1,$2,'admin')`, id, accountID); err != nil {
		return Archive{}, err
	}
	if err := s.audit(ctx, tx, id, accountID, "archive_created", &accountID, map[string]any{"kind": kind, "role": "admin", "canBulkExport": false}); err != nil {
		return Archive{}, err
	}
	if err := tx.Commit(); err != nil {
		return Archive{}, err
	}
	item := Archive{ID: id, Kind: kind, Name: name, Role: "admin"}
	if jurisdiction != "" {
		item.Jurisdiction = &jurisdiction
	}
	return item, nil
}

func (s *Service) List(ctx context.Context, accountID int64) ([]Archive, error) {
	tx, err := s.transaction(ctx, accountID, "")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT a.id,a.kind,a.name,a.jurisdiction,m.role,m.can_bulk_export
		FROM invoyis.archives a JOIN invoyis.archive_memberships m ON m.archive_id=a.id
		WHERE m.account_id=$1 ORDER BY a.created_at DESC,a.id DESC`, accountID)
	if err != nil {
		return nil, err
	}
	items := []Archive{}
	for rows.Next() {
		item, err := scanArchive(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return items, tx.Commit()
}

func (s *Service) Get(ctx context.Context, accountID int64, archiveID string) (Archive, error) {
	if !uuidPattern.MatchString(archiveID) {
		return Archive{}, ErrNotFound
	}
	tx, err := s.transaction(ctx, accountID, archiveID)
	if err != nil {
		return Archive{}, err
	}
	defer tx.Rollback()
	if _, _, err := s.membership(ctx, tx, archiveID, accountID, "view"); err != nil {
		return Archive{}, err
	}
	item, err := scanArchive(tx.QueryRowContext(ctx, `SELECT a.id,a.kind,a.name,a.jurisdiction,m.role,m.can_bulk_export
		FROM invoyis.archives a JOIN invoyis.archive_memberships m ON m.archive_id=a.id
		WHERE a.id=$1 AND m.account_id=$2`, archiveID, accountID))
	if err != nil {
		return Archive{}, err
	}
	return item, tx.Commit()
}

func (s *Service) Update(ctx context.Context, accountID int64, archiveID, name, jurisdiction string) (Archive, error) {
	if !uuidPattern.MatchString(archiveID) {
		return Archive{}, ErrNotFound
	}
	name = strings.TrimSpace(name)
	jurisdiction = strings.ToUpper(strings.TrimSpace(jurisdiction))
	if len(name) < 1 || len(name) > 120 || (jurisdiction != "" && !jurisdictionPattern.MatchString(jurisdiction)) {
		return Archive{}, ErrInvalid
	}
	tx, err := s.transaction(ctx, accountID, archiveID)
	if err != nil {
		return Archive{}, err
	}
	defer tx.Rollback()
	if _, _, err := s.membership(ctx, tx, archiveID, accountID, "settings"); err != nil {
		return Archive{}, err
	}
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM invoyis.archives WHERE id=$1 FOR UPDATE`, archiveID).Scan(&kind); err != nil {
		return Archive{}, err
	}
	if kind == "business" && jurisdiction == "" {
		return Archive{}, ErrInvalid
	}
	var selected any
	if jurisdiction != "" {
		selected = jurisdiction
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invoyis.archives SET name=$2,jurisdiction=$3 WHERE id=$1`, archiveID, name, selected); err != nil {
		return Archive{}, err
	}
	if err := s.audit(ctx, tx, archiveID, accountID, "archive_updated", nil, map[string]any{"name": name, "jurisdiction": jurisdiction}); err != nil {
		return Archive{}, err
	}
	if err := tx.Commit(); err != nil {
		return Archive{}, err
	}
	return s.Get(ctx, accountID, archiveID)
}

type Member struct {
	AccountID     int64  `json:"accountId"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	CanBulkExport bool   `json:"canBulkExport"`
}

func (s *Service) Members(ctx context.Context, accountID int64, archiveID string) ([]Member, error) {
	if !uuidPattern.MatchString(archiveID) {
		return nil, ErrNotFound
	}
	tx, err := s.transaction(ctx, accountID, archiveID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, _, err := s.membership(ctx, tx, archiveID, accountID, "members"); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.account_id,invoyis.archive_member_email(m.archive_id,m.account_id),m.role,m.can_bulk_export
		FROM invoyis.archive_memberships m WHERE m.archive_id=$1 ORDER BY m.created_at,m.account_id`, archiveID)
	if err != nil {
		return nil, err
	}
	items := []Member{}
	for rows.Next() {
		var item Member
		if err := rows.Scan(&item.AccountID, &item.Email, &item.Role, &item.CanBulkExport); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return items, tx.Commit()
}

func (s *Service) ChangeMember(ctx context.Context, actorID int64, archiveID string, targetID int64, role string, bulkExport bool) error {
	if !uuidPattern.MatchString(archiveID) || targetID <= 0 {
		return ErrNotFound
	}
	if !validRole(role) {
		return ErrInvalid
	}
	tx, err := s.transaction(ctx, actorID, archiveID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var lockedArchiveID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM invoyis.archives WHERE id=$1 FOR UPDATE`, archiveID).Scan(&lockedArchiveID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		return err
	}
	if _, _, err := s.membership(ctx, tx, archiveID, actorID, "members"); err != nil {
		return err
	}
	var oldRole string
	var oldGrant bool
	if err := tx.QueryRowContext(ctx, `SELECT role,can_bulk_export FROM invoyis.archive_memberships
		WHERE archive_id=$1 AND account_id=$2 FOR UPDATE`, archiveID, targetID).Scan(&oldRole, &oldGrant); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if oldRole == "admin" && role != "admin" {
		var adminCount int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM invoyis.archive_memberships
			WHERE archive_id=$1 AND role='admin'`, archiveID).Scan(&adminCount); err != nil {
			return err
		}
		if adminCount <= 1 {
			return ErrConflict
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invoyis.archive_memberships SET role=$3,can_bulk_export=$4
		WHERE archive_id=$1 AND account_id=$2`, archiveID, targetID, role, bulkExport); err != nil {
		return err
	}
	if err := s.audit(ctx, tx, archiveID, actorID, "member_changed", &targetID,
		map[string]any{"oldRole": oldRole, "role": role, "oldCanBulkExport": oldGrant, "canBulkExport": bulkExport}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) RemoveMember(ctx context.Context, actorID int64, archiveID string, targetID int64) error {
	if !uuidPattern.MatchString(archiveID) || targetID <= 0 {
		return ErrNotFound
	}
	tx, err := s.transaction(ctx, actorID, archiveID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var lockedArchiveID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM invoyis.archives WHERE id=$1 FOR UPDATE`, archiveID).Scan(&lockedArchiveID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		return err
	}
	if _, _, err := s.membership(ctx, tx, archiveID, actorID, "members"); err != nil {
		return err
	}
	var role string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM invoyis.archive_memberships
		WHERE archive_id=$1 AND account_id=$2 FOR UPDATE`, archiveID, targetID).Scan(&role); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if role == "admin" {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM invoyis.archive_memberships WHERE archive_id=$1 AND role='admin'`, archiveID).Scan(&count); err != nil {
			return err
		}
		if count <= 1 {
			return ErrConflict
		}
	}
	var targetEmail string
	if err := tx.QueryRowContext(ctx, `SELECT invoyis.archive_member_email($1,$2)`, archiveID, targetID).Scan(&targetEmail); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE invoyis.archive_invitations SET revoked_at=$3
		WHERE archive_id=$1 AND email=$2 AND consumed_at IS NULL AND revoked_at IS NULL`, archiveID, targetEmail, s.Now().UTC())
	if err != nil {
		return err
	}
	revokedCount, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if revokedCount > 0 {
		if err := s.audit(ctx, tx, archiveID, actorID, "invitations_revoked_on_removal", &targetID, map[string]any{"count": revokedCount}); err != nil {
			return err
		}
	}
	if err := s.audit(ctx, tx, archiveID, actorID, "member_removed", &targetID, map[string]any{"role": role}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM invoyis.archive_memberships WHERE archive_id=$1 AND account_id=$2`, archiveID, targetID); err != nil {
		return err
	}
	return tx.Commit()
}

type Invitation struct {
	ID        string `json:"id"`
	ArchiveID string `json:"archiveId"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expiresAt"`
	Status    string `json:"status"`
}

func invitationMessage(language, archiveName, link string) (string, string) {
	language = strings.ToLower(strings.TrimSpace(strings.Split(language, ",")[0]))
	if len(language) >= 2 {
		language = language[:2]
	}
	switch language {
	case "fr":
		return "Invitation Invoyis", "Vous avez été invité à l'archive « " + archiveName + " ». Ouvrez ce lien pour vous connecter et accepter l'invitation : " + link
	case "he":
		return "הזמנה ל-Invoyis", "הוזמנת לארכיון \"" + archiveName + "\". יש לפתוח את הקישור, להתחבר ולאשר את ההזמנה: " + link
	default:
		return "Your Invoyis invitation", "You have been invited to the " + archiveName + " archive. Open this link, sign in, and accept: " + link
	}
}

func (s *Service) Invite(ctx context.Context, actorID int64, archiveID, rawEmail, role, language string) (Invitation, error) {
	if !uuidPattern.MatchString(archiveID) {
		return Invitation{}, ErrNotFound
	}
	email, err := auth.NormalizeEmail(rawEmail)
	if err != nil || !validRole(role) {
		return Invitation{}, ErrInvalid
	}
	id, err := newID()
	if err != nil {
		return Invitation{}, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return Invitation{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	digest := sha256.Sum256([]byte(token))
	expiry := s.Now().UTC().Add(7 * 24 * time.Hour)
	tx, err := s.transaction(ctx, actorID, archiveID)
	if err != nil {
		return Invitation{}, err
	}
	defer tx.Rollback()
	var archiveName string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM invoyis.archives WHERE id=$1 FOR UPDATE`, archiveID).Scan(&archiveName); errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, ErrForbidden
	} else if err != nil {
		return Invitation{}, err
	}
	if _, _, err := s.membership(ctx, tx, archiveID, actorID, "invite"); err != nil {
		return Invitation{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO invoyis.archive_invitations
		(id,archive_id,email,role,token_digest,invited_by,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, archiveID, email, role, digest[:], actorID, expiry); err != nil {
		return Invitation{}, err
	}
	if err := s.audit(ctx, tx, archiveID, actorID, "invitation_created", nil, map[string]any{"email": email, "role": role}); err != nil {
		return Invitation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Invitation{}, err
	}
	link := s.BrowserOrigin + "/invitations/accept#token=" + token
	subject, body := invitationMessage(language, archiveName, link)
	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = s.Sender.SendText(sendCtx, email, subject, body)
	cancel()
	if err != nil {
		_ = s.RevokeInvitation(context.Background(), actorID, archiveID, id)
		return Invitation{}, &deliveryFailure{}
	}
	return Invitation{ID: id, ArchiveID: archiveID, Email: email, Role: role, ExpiresAt: expiry.Format(time.RFC3339), Status: "active"}, nil
}

type deliveryFailure struct{}

func (*deliveryFailure) Error() string { return "invitation delivery failed" }

func (s *Service) Invitations(ctx context.Context, actorID int64, archiveID string) ([]Invitation, error) {
	if !uuidPattern.MatchString(archiveID) {
		return nil, ErrNotFound
	}
	tx, err := s.transaction(ctx, actorID, archiveID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, _, err := s.membership(ctx, tx, archiveID, actorID, "members"); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,archive_id,email,role,expires_at,revoked_at,consumed_at
		FROM invoyis.archive_invitations WHERE archive_id=$1 ORDER BY created_at DESC,id DESC`, archiveID)
	if err != nil {
		return nil, err
	}
	items := []Invitation{}
	for rows.Next() {
		var item Invitation
		var expiry time.Time
		var revoked, consumed sql.NullTime
		if err := rows.Scan(&item.ID, &item.ArchiveID, &item.Email, &item.Role, &expiry, &revoked, &consumed); err != nil {
			rows.Close()
			return nil, err
		}
		item.ExpiresAt = expiry.UTC().Format(time.RFC3339)
		item.Status = "active"
		if revoked.Valid {
			item.Status = "revoked"
		} else if consumed.Valid {
			item.Status = "accepted"
		} else if !s.Now().Before(expiry) {
			item.Status = "expired"
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return items, tx.Commit()
}

func (s *Service) RevokeInvitation(ctx context.Context, actorID int64, archiveID, invitationID string) error {
	if !uuidPattern.MatchString(archiveID) || !uuidPattern.MatchString(invitationID) {
		return ErrNotFound
	}
	tx, err := s.transaction(ctx, actorID, archiveID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, _, err := s.membership(ctx, tx, archiveID, actorID, "invite"); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE invoyis.archive_invitations SET revoked_at=$3
		WHERE id=$1 AND archive_id=$2 AND revoked_at IS NULL AND consumed_at IS NULL`, invitationID, archiveID, s.Now().UTC())
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	if err := s.audit(ctx, tx, archiveID, actorID, "invitation_revoked", nil, map[string]any{"invitationId": invitationID}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) Accept(ctx context.Context, accountID int64, token string) (Archive, error) {
	if len(token) != 43 {
		return Archive{}, ErrInvalid
	}
	if _, err := base64.RawURLEncoding.DecodeString(token); err != nil {
		return Archive{}, ErrInvalid
	}
	digest := sha256.Sum256([]byte(token))
	tx, err := s.transaction(ctx, accountID, "")
	if err != nil {
		return Archive{}, err
	}
	defer tx.Rollback()
	var archiveID, role string
	err = tx.QueryRowContext(ctx, `SELECT redeemed_archive_id,redeemed_role
		FROM invoyis.redeem_archive_invitation($1,$2)`, digest[:], accountID).Scan(&archiveID, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return Archive{}, ErrConflict
	}
	if err != nil {
		if strings.Contains(err.Error(), "SQLSTATE 23505") {
			return Archive{}, ErrConflict
		}
		return Archive{}, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('invoyis.archive_id',$1,true)`, archiveID); err != nil {
		return Archive{}, err
	}
	if err := s.audit(ctx, tx, archiveID, accountID, "invitation_accepted", &accountID, map[string]any{"role": role, "canBulkExport": false}); err != nil {
		return Archive{}, err
	}
	if err := tx.Commit(); err != nil {
		return Archive{}, err
	}
	return s.Get(ctx, accountID, archiveID)
}

func (s *Service) Language(ctx context.Context, accountID int64) (string, error) {
	var language string
	err := s.Auth.DB.QueryRowContext(ctx, `SELECT interface_language FROM invoyis.accounts WHERE id=$1`, accountID).Scan(&language)
	return language, err
}

func (s *Service) SetLanguage(ctx context.Context, accountID int64, language string) error {
	if language != "en" && language != "fr" && language != "he" {
		return ErrInvalid
	}
	_, err := s.Auth.DB.ExecContext(ctx, `UPDATE invoyis.accounts SET interface_language=$2 WHERE id=$1`, accountID, language)
	return err
}
