package flight

import (
	"testing"
	"time"
)

func TestDailySnapshotKeyUsesShanghaiDate(t *testing.T) {
	now := time.Date(2026, 4, 28, 17, 30, 0, 0, time.UTC)
	key := DailySnapshotKey("szx", "departure", now)

	want := "flights/szx/departure/daily/2026-04-29.json"
	if key != want {
		t.Fatalf("expected key %q, got %q", want, key)
	}
}
