package flight

import (
	"testing"
	"time"
)

func TestDailySnapshotLatestKeyUsesShanghaiDate(t *testing.T) {
	now := time.Date(2026, 4, 28, 17, 30, 0, 0, time.UTC)
	key := DailySnapshotLatestKey("szx", "departure", now)

	want := "flights/szx/departure/daily/2026-04-29/latest.json"
	if key != want {
		t.Fatalf("expected key %q, got %q", want, key)
	}
}

func TestDailySnapshotVersionedKeyUsesShanghaiClock(t *testing.T) {
	now := time.Date(2026, 4, 28, 17, 30, 0, 0, time.UTC)
	key := DailySnapshotVersionedKey("szx", "arrival", now)

	want := "flights/szx/arrival/daily/2026-04-29/1-30.json"
	if key != want {
		t.Fatalf("expected key %q, got %q", want, key)
	}
}
