package archive

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/auth"
)

func TestInvitationAttemptLimit(t *testing.T) {
	transient := errors.New("synthetic transient failure")
	if finalInvitationAttempt(transient, 8) {
		t.Fatal("ninth attempt stopped early")
	}
	if !finalInvitationAttempt(transient, 9) {
		t.Fatal("tenth attempt still retries")
	}
	if !finalInvitationAttempt(&auth.PermanentDeliveryError{}, 0) {
		t.Fatal("permanent rejection still retries")
	}
	if !finalInvitationAttempt(nil, 0) {
		t.Fatal("successful delivery still queued")
	}
}

func TestInvitationRetryAfterUsesLimitWindow(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeFailure(recorder, &rateLimitFailure{retryAfter: 30 * 24 * time.Hour}, "synthetic-request-id")
	if recorder.Code != 429 || recorder.Header().Get("Retry-After") != "2592000" {
		t.Fatalf("status=%d retry=%q", recorder.Code, recorder.Header().Get("Retry-After"))
	}
}
