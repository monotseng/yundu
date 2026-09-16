package downloads

import (
	"testing"
	"time"
)

func TestDownloadableRequiresTargetPortalAndLiveState(t *testing.T) {
	now := time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	if !downloadable("PROD_TO_OFFICE", "OFFICE", "READY", future, now) {
		t.Fatal("expected office target to be downloadable")
	}
	if downloadable("PROD_TO_OFFICE", "PRODUCTION", "READY", future, now) {
		t.Fatal("source portal must be denied")
	}
	if downloadable("OFFICE_TO_PROD", "PRODUCTION", "REVOKED", future, now) {
		t.Fatal("revoked request must be denied")
	}
	if downloadable("OFFICE_TO_PROD", "PRODUCTION", "READY", now, now) {
		t.Fatal("expiry boundary must be denied")
	}
}
