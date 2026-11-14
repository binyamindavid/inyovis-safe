package archive

import (
	"testing"
	"time"

	"github.com/binyamindavid/inyovis-safe/apps/backend/internal/retention"
)

func TestShreddingReviewCalendarBoundaryAndBlockers(t *testing.T) {
	current := CurrentRetentionResult{State: "current", Snapshot: &RetentionSnapshot{Outcome: retention.Outcome{Status: "calculated", KeepThrough: "2026-11-14"}}}
	for _, tc := range []struct {
		name, kind, role, state string
		active                  int
		day                     string
		want                    string
	}{
		{"inclusive day", "business", "admin", "current", 0, "2026-11-14", "keep_through_date"},
		{"following day", "business", "admin", "current", 0, "2026-11-15", ""},
		{"hold", "business", "admin", "current", 1, "2026-11-15", "active_hold"},
		{"viewer", "business", "viewer", "current", 0, "2026-11-15", "reviewer_permission_required"},
		{"stale", "business", "admin", "stale", 0, "2026-11-15", "retention_stale"},
		{"personal", "personal", "admin", "current", 0, "2026-11-15", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := current
			v.State = tc.state
			day, _ := time.Parse("2006-01-02", tc.day)
			got := reviewBlocker(tc.kind, tc.role, true, v, tc.active, day)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
