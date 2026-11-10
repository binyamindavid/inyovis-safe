package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const testGateway = "synthetic-gateway-secret-32-characters"
const testOrigin = "https://localhost:3001"
const testEmail = "pilot@example.test"

type captureSender struct {
	mu    sync.Mutex
	code  string
	sends int
	fail  bool
}

type senderFunc func(context.Context, string, string, string) error

func (f senderFunc) Send(ctx context.Context, email, language, code string) error {
	return f(ctx, email, language, code)
}
func (f senderFunc) SendText(ctx context.Context, email, subject, body string) error {
	return f(ctx, email, subject, body)
}

func (s *captureSender) Send(_ context.Context, _, _, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends++
	if s.fail {
		return context.DeadlineExceeded
	}
	s.code = code
	return nil
}
func (s *captureSender) SendText(_ context.Context, _, _, _ string) error { return nil }
func (s *captureSender) Code() string                                     { s.mu.Lock(); defer s.mu.Unlock(); return s.code }

func testService(t *testing.T) (*Service, *captureSender, http.Handler) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(parsed.Path, "_test") {
		t.Fatal("refusing to run auth tests outside a _test database")
	}
	sender := &captureSender{}
	s, err := New(dsn, strings.Repeat("ab", 32), testGateway, testOrigin, sender)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.DB.Exec(`TRUNCATE invoyis.auth_delivery_outbox, invoyis.auth_challenges, invoyis.sessions, invoyis.auth_rate_limits, invoyis.accounts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO invoyis.accounts (email) VALUES ($1)`, testEmail); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	return s, sender, mux
}

func call(mux http.Handler, method, path string, body any, origin, cookie string) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("X-Request-ID", "123e4567-e89b-42d3-a456-426614174000")
	req.Header.Set("X-Invoyis-Gateway-Key", testGateway)
	req.Header.Set("X-Invoyis-Client-IP", "127.0.0.1")
	if method == http.MethodPost {
		req.Header.Set("Origin", origin)
		req.Header.Set("X-Invoyis-Intent", "browser")
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func requestCode(mux http.Handler, email string) *httptest.ResponseRecorder {
	return call(mux, "POST", "/v1/auth/code/request", map[string]string{"email": email}, testOrigin, "")
}
func verifyCode(mux http.Handler, email, code string) *httptest.ResponseRecorder {
	return call(mux, "POST", "/v1/auth/code/verify", map[string]string{"email": email, "code": code}, testOrigin, "")
}

func TestSignInLifecycle(t *testing.T) {
	s, sender, mux := testService(t)
	eligible := requestCode(mux, testEmail)
	ineligible := requestCode(mux, "unknown@example.test")
	if eligible.Code != 202 || ineligible.Code != 202 || eligible.Body.String() != ineligible.Body.String() {
		t.Fatalf("request responses differ: %d %q / %d %q", eligible.Code, eligible.Body.String(), ineligible.Code, ineligible.Body.String())
	}
	if _, err := s.deliverBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	code := sender.Code()
	if len(code) != 6 {
		t.Fatal("no six-digit code delivered")
	}
	if response := verifyCode(mux, testEmail, "000000"); response.Code != 422 {
		t.Fatalf("invalid code status %d", response.Code)
	}
	first := verifyCode(mux, testEmail, code)
	if first.Code != 200 {
		t.Fatalf("verify status %d: %s", first.Code, first.Body.String())
	}
	cookie := first.Result().Cookies()[0]
	if cookie.Name != CookieName || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Domain != "" {
		t.Fatalf("unsafe cookie: %#v", cookie)
	}
	if response := verifyCode(mux, testEmail, code); response.Code != 422 {
		t.Fatalf("reused code status %d", response.Code)
	}
	if response := call(mux, "GET", "/v1/auth/session", nil, "", cookie.String()); response.Code != 200 {
		t.Fatalf("restore status %d", response.Code)
	}
	if response := call(mux, "POST", "/v1/auth/logout", nil, testOrigin, cookie.String()); response.Code != 204 || len(response.Result().Cookies()) != 1 {
		t.Fatalf("logout status %d", response.Code)
	}
	if response := call(mux, "GET", "/v1/auth/session", nil, "", cookie.String()); response.Code != 401 {
		t.Fatalf("revoked replay status %d", response.Code)
	}
}

func TestResendExpiryAndConcurrentRedemption(t *testing.T) {
	s, sender, mux := testService(t)
	requestCode(mux, testEmail)
	if _, err := s.deliverBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldCode := sender.Code()
	requestCode(mux, testEmail)
	if _, err := s.deliverBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	newCode := sender.Code()
	if response := verifyCode(mux, testEmail, oldCode); response.Code != 422 {
		t.Fatalf("old code status %d", response.Code)
	}
	var wait sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wait.Add(1)
		go func() { defer wait.Done(); statuses <- verifyCode(mux, testEmail, newCode).Code }()
	}
	wait.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[422] != 1 {
		t.Fatalf("concurrent redemption: %#v", counts)
	}
	requestCode(mux, testEmail)
	if _, err := s.deliverBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE invoyis.auth_challenges SET expires_at=$1 WHERE email=$2 AND consumed_at IS NULL`, time.Now().Add(-time.Minute), testEmail); err != nil {
		t.Fatal(err)
	}
	if response := verifyCode(mux, testEmail, sender.Code()); response.Code != 422 || !strings.Contains(response.Body.String(), "EXPIRED_CODE") {
		t.Fatalf("expired code: %d %s", response.Code, response.Body.String())
	}
}

func TestRateLimitsAndOrigin(t *testing.T) {
	s, sender, mux := testService(t)
	if response := call(mux, "POST", "/v1/auth/code/request", map[string]string{"email": testEmail}, "https://evil.example", ""); response.Code != 403 {
		t.Fatalf("cross-origin status %d", response.Code)
	}
	missingIntent := httptest.NewRequest("POST", "/v1/auth/code/request", strings.NewReader(`{"email":"pilot@example.test"}`))
	missingIntent.Header.Set("Origin", testOrigin)
	missingIntent.Header.Set("X-Invoyis-Gateway-Key", testGateway)
	missingIntent.Header.Set("X-Invoyis-Client-IP", "127.0.0.1")
	missingIntent.Header.Set("Content-Type", "application/json")
	missingResponse := httptest.NewRecorder()
	mux.ServeHTTP(missingResponse, missingIntent)
	if missingResponse.Code != 403 {
		t.Fatalf("missing mutation header status %d", missingResponse.Code)
	}
	for i := range 5 {
		if response := requestCode(mux, testEmail); response.Code != 202 {
			t.Fatalf("request %d: %d", i, response.Code)
		}
	}
	if response := requestCode(mux, testEmail); response.Code != 429 || response.Header().Get("Retry-After") == "" {
		t.Fatalf("request limit status %d", response.Code)
	}
	// Verification budget is shared across resends and persisted by email and IP.
	for i := range 10 {
		if response := verifyCode(mux, testEmail, "000000"); response.Code != 422 {
			t.Fatalf("verify %d: %d", i, response.Code)
		}
	}
	if response := verifyCode(mux, testEmail, "000000"); response.Code != 429 {
		t.Fatalf("verify limit status %d", response.Code)
	}
	if _, err := s.DB.Exec(`UPDATE invoyis.auth_rate_limits SET window_start=now()-interval '16 minutes' WHERE kind='verify-email'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE invoyis.auth_rate_limits SET hits=50 WHERE kind='verify-email-day'`); err != nil {
		t.Fatal(err)
	}
	if response := verifyCode(mux, testEmail, "000000"); response.Code != 429 {
		t.Fatalf("daily verify limit status %d", response.Code)
	}
	if sender.sends != 0 {
		t.Fatal("delivery ran during request handler")
	}
	for i := range 26 {
		limited, err := s.RequestCode(context.Background(), fmt.Sprintf("other%d@example.test", i), "127.0.0.1", "en")
		if err != nil || limited != (i == 25) {
			t.Fatalf("IP limit at %d: %v, %v", i, limited, err)
		}
	}
}

func TestSessionExpiryAndFailedDeliveryRetry(t *testing.T) {
	s, sender, mux := testService(t)
	sender.fail = true
	requestCode(mux, testEmail)
	if _, err := s.deliverBatch(context.Background()); err == nil {
		t.Fatal("expected delivery failure")
	}
	sender.fail = false
	if _, err := s.DB.Exec(`UPDATE invoyis.auth_delivery_outbox SET next_attempt_at=now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.deliverBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	response := verifyCode(mux, testEmail, sender.Code())
	if response.Code != 200 {
		t.Fatalf("verify status %d", response.Code)
	}
	cookie := response.Result().Cookies()[0]
	if _, err := s.DB.Exec(`UPDATE invoyis.sessions SET expires_at=now()-interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if result := call(mux, "GET", "/v1/auth/session", nil, "", cookie.String()); result.Code != 401 {
		t.Fatalf("expired session status %d", result.Code)
	}
}

func TestOutboxClaimsBatchAndSendsOutsideTransaction(t *testing.T) {
	s, _, _ := testService(t)
	for i := range 20 {
		email := fmt.Sprintf("queued%d@example.test", i)
		if _, err := s.DB.Exec(`INSERT INTO invoyis.accounts (email) VALUES ($1)`, email); err != nil {
			t.Fatal(err)
		}
		if limited, err := s.RequestCode(context.Background(), email, "198.51.100.4", "en"); err != nil || limited {
			t.Fatalf("queue request %d: limited=%v error=%v", i, limited, err)
		}
	}
	var sent int
	var mu sync.Mutex
	s.Sender = senderFunc(func(ctx context.Context, email, _, _ string) error {
		var token string
		if err := s.DB.QueryRowContext(ctx, `SELECT o.claim_token FROM invoyis.auth_delivery_outbox o
			JOIN invoyis.auth_challenges c ON c.id=o.challenge_id WHERE c.email=$1 FOR UPDATE OF o NOWAIT`, email).Scan(&token); err != nil {
			return err
		}
		mu.Lock()
		sent++
		mu.Unlock()
		return nil
	})
	first, err := s.deliverBatch(context.Background())
	if err != nil || first != 16 {
		t.Fatalf("first batch=%d, error=%v", first, err)
	}
	second, err := s.deliverBatch(context.Background())
	if err != nil || second != 4 {
		t.Fatalf("second batch=%d, error=%v", second, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if sent != 20 {
		t.Fatalf("sent %d messages, want 20", sent)
	}
	var pending int
	if err := s.DB.QueryRow(`SELECT count(*) FROM invoyis.auth_delivery_outbox`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("outbox pending=%d, error=%v", pending, err)
	}
}

func TestAuthCleanupRemovesOldRows(t *testing.T) {
	s, _, mux := testService(t)
	requestCode(mux, testEmail)
	if _, err := s.DB.Exec(`UPDATE invoyis.auth_challenges SET created_at=now()-interval '9 days', expires_at=now()-interval '8 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE invoyis.auth_rate_limits SET window_start=now()-interval '9 days'`); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupAuth(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"auth_challenges", "auth_delivery_outbox", "auth_rate_limits"} {
		var count int
		if err := s.DB.QueryRow(`SELECT count(*) FROM invoyis.` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d error=%v", table, count, err)
		}
	}
}

func TestUndecryptableDeliveryIsDiscarded(t *testing.T) {
	s, sender, mux := testService(t)
	if response := requestCode(mux, testEmail); response.Code != 202 {
		t.Fatalf("request status %d", response.Code)
	}
	if _, err := s.DB.Exec(`UPDATE invoyis.auth_delivery_outbox SET encrypted_code=decode('00','hex')`); err != nil {
		t.Fatal(err)
	}
	if count, err := s.deliverBatch(context.Background()); count != 1 || deliveryPhase(err) != "decrypt" {
		t.Fatalf("batch count=%d, error=%v", count, err)
	}
	var pending int
	if err := s.DB.QueryRow(`SELECT count(*) FROM invoyis.auth_delivery_outbox`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("undecryptable message remains queued: count=%d error=%v", pending, err)
	}
	if sender.sends != 0 {
		t.Fatal("undecryptable code was sent")
	}
}
