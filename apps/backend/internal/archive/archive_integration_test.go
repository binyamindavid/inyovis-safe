package archive

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/auth"
)

type memorySender struct {
	mu    sync.Mutex
	links map[string]string
	codes map[string]string
}

func (m *memorySender) Send(_ context.Context, email, _, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codes[email] = code
	return nil
}
func (m *memorySender) SendText(_ context.Context, email, _, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, word := range strings.Fields(body) {
		if strings.Contains(word, "#token=") {
			m.links[email] = strings.TrimSpace(strings.TrimSuffix(word, "."))
		}
	}
	return nil
}
func (m *memorySender) token(email string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	parts := strings.Split(m.links[email], "#token=")
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}
func (m *memorySender) code(email string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.codes[email]
}

func setup(t *testing.T) (*Service, *memorySender, *sql.DB, int64) {
	t.Helper()
	ownerURL := os.Getenv("ARCHIVE_TEST_DATABASE_URL")
	runtimeURL := os.Getenv("TEST_ARCHIVE_DATABASE_URL")
	if ownerURL == "" || runtimeURL == "" {
		t.Skip("archive test database is not configured")
	}
	parsed, err := url.Parse(ownerURL)
	if err != nil || !strings.HasSuffix(parsed.Path, "_test") {
		t.Fatal("refusing to use a non-test archive database")
	}
	owner, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close() })
	if _, err := owner.Exec(`TRUNCATE invoyis.archive_audit,invoyis.archive_invitations,invoyis.archive_memberships,
		invoyis.archives,invoyis.auth_delivery_outbox,invoyis.auth_challenges,invoyis.sessions,
		invoyis.auth_rate_limits,invoyis.accounts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	sender := &memorySender{links: map[string]string{}, codes: map[string]string{}}
	authService, err := auth.New(ownerURL, strings.Repeat("ab", 32), "synthetic-gateway-secret-32-characters", "https://localhost:3000", sender)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { authService.Close() })
	s, err := New(runtimeURL, authService, sender, "https://localhost:3000")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	var accountID int64
	if err := owner.QueryRow(`INSERT INTO invoyis.accounts (email) VALUES ('admin@example.test') RETURNING id`).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	return s, sender, owner, accountID
}

func addAccount(t *testing.T, owner *sql.DB, email string) int64 {
	t.Helper()
	var id int64
	if err := owner.QueryRow(`INSERT INTO invoyis.accounts (email) VALUES ($1) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestArchiveCreationRLSAndLanguage(t *testing.T) {
	s, _, owner, adminID := setup(t)
	ctx := context.Background()
	personal, err := s.Create(ctx, adminID, "personal", "Home", "")
	if err != nil {
		t.Fatal(err)
	}
	business, err := s.Create(ctx, adminID, "business", "Company", "IL")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, adminID, "business", "Missing jurisdiction", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("business jurisdiction: %v", err)
	}
	if personal.CanBulkExport || business.CanBulkExport || personal.Role != "admin" {
		t.Fatal("initial admin grant is wrong")
	}
	items, err := s.List(ctx, adminID)
	if err != nil || len(items) != 2 {
		t.Fatalf("archive list: %v %#v", err, items)
	}
	outsiderID := addAccount(t, owner, "outsider@example.test")
	if _, err := s.Get(ctx, outsiderID, personal.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider read: %v", err)
	}
	if _, err := s.Update(ctx, outsiderID, business.ID, "Changed", "FR"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider write: %v", err)
	}
	isolated, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolated.ExecContext(ctx, `SELECT set_config('invoyis.account_id',$1,true),set_config('invoyis.archive_id',$2,true)`, strconv.FormatInt(outsiderID, 10), business.ID); err != nil {
		t.Fatal(err)
	}
	result, err := isolated.ExecContext(ctx, `UPDATE invoyis.archives SET name='Cross archive' WHERE id=$1`, business.ID)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 0 {
		t.Fatalf("RLS allowed cross archive update: %d %v", changed, err)
	}
	if _, err := isolated.ExecContext(ctx, `INSERT INTO invoyis.archive_memberships (archive_id,account_id,role) VALUES ($1,$2,'viewer')`, business.ID, outsiderID); err == nil {
		t.Fatal("RLS allowed outsider membership insert")
	}
	_ = isolated.Rollback()
	if err := s.SetLanguage(ctx, adminID, "he"); err != nil {
		t.Fatal(err)
	}
	if language, err := s.Language(ctx, adminID); err != nil || language != "he" {
		t.Fatalf("language: %s %v", language, err)
	}
	after, err := s.Get(ctx, adminID, business.ID)
	if err != nil || after.Jurisdiction == nil || *after.Jurisdiction != "IL" {
		t.Fatalf("language changed jurisdiction: %#v %v", after, err)
	}
	var bypass, ownerRole bool
	if err := s.DB.QueryRow(`SELECT r.rolbypassrls,pg_get_userbyid(c.relowner)=current_user FROM pg_roles r
		JOIN pg_class c ON c.oid='invoyis.archives'::regclass WHERE r.rolname=current_user`).Scan(&bypass, &ownerRole); err != nil || bypass || ownerRole {
		t.Fatalf("runtime role bypasses RLS: %v %v %v", bypass, ownerRole, err)
	}
	var visible int
	if err := s.DB.QueryRow(`SELECT count(*) FROM invoyis.archives`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("missing context exposed %d rows: %v", visible, err)
	}
	connection, err := s.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	tx, err := connection.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('invoyis.account_id',$1,true)`, "1"); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM invoyis.archives`).Scan(&visible); err != nil || visible != 2 {
		t.Fatalf("identity context saw %d: %v", visible, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := connection.QueryRowContext(ctx, `SELECT count(*) FROM invoyis.archives`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("pooled context leaked %d: %v", visible, err)
	}
}

func TestRolesLastAdminAndRevocation(t *testing.T) {
	s, _, owner, adminID := setup(t)
	ctx := context.Background()
	item, err := s.Create(ctx, adminID, "personal", "Shared", "")
	if err != nil {
		t.Fatal(err)
	}
	viewerID := addAccount(t, owner, "viewer@example.test")
	contributorID := addAccount(t, owner, "contributor@example.test")
	// Seed memberships as the migration owner; mutation paths are exercised below.
	if _, err := owner.Exec(`INSERT INTO invoyis.archive_memberships (archive_id,account_id,role) VALUES ($1,$2,'viewer'),($1,$3,'contributor')`, item.ID, viewerID, contributorID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, viewerID, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Members(ctx, viewerID, item.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer members: %v", err)
	}
	if _, err := s.Update(ctx, contributorID, item.ID, "No", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("contributor settings: %v", err)
	}
	if Can("admin", false, "bulk_export") || Can("viewer", false, "bulk_export") || !Can("viewer", true, "bulk_export") || Can("viewer", true, "submit") {
		t.Fatal("permission map incorrect")
	}
	if err := s.ChangeMember(ctx, adminID, item.ID, viewerID, "viewer", true); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeMember(ctx, adminID, item.ID, contributorID, "admin", false); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsFound := make(chan error, 2)
	for _, target := range []int64{adminID, contributorID} {
		wait.Add(1)
		go func(target int64) {
			defer wait.Done()
			errorsFound <- s.ChangeMember(ctx, adminID, item.ID, target, "viewer", false)
		}(target)
	}
	wait.Wait()
	close(errorsFound)
	var successes, conflicts int
	for err := range errorsFound {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) || errors.Is(err, ErrForbidden) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent last admin: success=%d conflict=%d", successes, conflicts)
	}
	var remainingAdmin int64
	if err := owner.QueryRow(`SELECT account_id FROM invoyis.archive_memberships WHERE archive_id=$1 AND role='admin'`, item.ID).Scan(&remainingAdmin); err != nil {
		t.Fatal(err)
	}
	revokedID := viewerID
	if remainingAdmin == viewerID {
		revokedID = adminID
	}
	if err := s.RemoveMember(ctx, remainingAdmin, item.ID, revokedID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, revokedID, item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked member still sees archive: %v", err)
	}
	var auditCount int
	if err := owner.QueryRow(`SELECT count(*) FROM invoyis.archive_audit WHERE archive_id=$1`, item.ID).Scan(&auditCount); err != nil || auditCount < 4 {
		t.Fatalf("audit count=%d error=%v", auditCount, err)
	}
}

func TestInvitationEnrollmentReplayAndExpiry(t *testing.T) {
	s, sender, owner, adminID := setup(t)
	ctx := context.Background()
	item, err := s.Create(ctx, adminID, "business", "Pilot", "IL")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := s.Invite(ctx, adminID, item.ID, " NEW@example.test ", "contributor", "fr")
	if err != nil {
		t.Fatal(err)
	}
	token := sender.token("new@example.test")
	if len(token) != 43 {
		t.Fatal("no invitation token in synthetic email")
	}
	limited, err := s.Auth.RequestCode(ctx, "new@example.test", "198.51.100.7", "fr")
	if err != nil || limited {
		t.Fatalf("invited code request: %v %v", limited, err)
	}
	// Code delivery is still handled by the existing auth outbox.
	workerContext, stop := context.WithCancel(ctx)
	go s.Auth.RunOutbox(workerContext)
	defer stop()
	deadline := time.Now().Add(3 * time.Second)
	code := sender.code("new@example.test")
	for code == "" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		code = sender.code("new@example.test")
	}
	if code == "" {
		t.Fatal("invited code was not delivered")
	}
	verification, limited, err := s.Auth.VerifyCode(ctx, "new@example.test", code, "198.51.100.7")
	if err != nil || limited {
		t.Fatalf("invite enrolment verify: %v %v", limited, err)
	}
	var inviteeID int64
	if err := owner.QueryRow(`SELECT id FROM invoyis.accounts WHERE email='new@example.test'`).Scan(&inviteeID); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.Accept(ctx, inviteeID, "new@example.test", token)
	if err != nil || accepted.ID != item.ID || accepted.Role != "contributor" {
		t.Fatalf("accept: %#v %v", accepted, err)
	}
	if _, _, _, err := s.Auth.SessionAccount(ctx, verification.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(ctx, inviteeID, "new@example.test", token); !errors.Is(err, ErrConflict) {
		t.Fatalf("replay: %v", err)
	}
	otherID := addAccount(t, owner, "other@example.test")
	if _, err := s.Accept(ctx, otherID, "other@example.test", token); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong email: %v", err)
	}
	second, err := s.Invite(ctx, adminID, item.ID, "other@example.test", "viewer", "en")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(`UPDATE invoyis.archive_invitations SET expires_at=now()-interval '1 minute' WHERE id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(ctx, otherID, "other@example.test", sender.token("other@example.test")); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired invite: %v", err)
	}
	third, err := s.Invite(ctx, adminID, item.ID, "other@example.test", "viewer", "en")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeInvitation(ctx, adminID, item.ID, third.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(ctx, otherID, "other@example.test", sender.token("other@example.test")); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoked invite: %v", err)
	}
	if invite.Status != "active" {
		t.Fatal("token leaked in response")
	}
	if _, err := s.Auth.RequestCode(ctx, "unknown@example.test", "198.51.100.8", "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Invite(ctx, adminID, item.ID, "race@example.test", "viewer", "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Auth.RequestCode(ctx, "race@example.test", "198.51.100.9", "en"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	raceCode := sender.code("race@example.test")
	for raceCode == "" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		raceCode = sender.code("race@example.test")
	}
	if raceCode == "" {
		t.Fatal("race code was not delivered")
	}
	if _, err := owner.Exec(`INSERT INTO invoyis.accounts (email,eligible) VALUES ('race@example.test',false)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Auth.VerifyCode(ctx, "race@example.test", raceCode, "198.51.100.9"); !errors.Is(err, auth.ErrInvalidCode) {
		t.Fatalf("ineligible account was enrolled: %v", err)
	}
}

func TestInvitationConcurrentAcceptance(t *testing.T) {
	s, sender, owner, adminID := setup(t)
	ctx := context.Background()
	item, err := s.Create(ctx, adminID, "personal", "Shared", "")
	if err != nil {
		t.Fatal(err)
	}
	inviteeID := addAccount(t, owner, "invitee@example.test")
	_, err = s.Invite(ctx, adminID, item.ID, "invitee@example.test", "viewer", "en")
	if err != nil {
		t.Fatal(err)
	}
	token := sender.token("invitee@example.test")
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := s.Accept(ctx, inviteeID, "invitee@example.test", token)
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	var success, rejected int
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || rejected != 1 {
		t.Fatalf("concurrent acceptance: success=%d rejected=%d", success, rejected)
	}
}
