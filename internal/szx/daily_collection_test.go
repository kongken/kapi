package szx

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestBuildDailySnapshotBuildsCanonicalResponse(t *testing.T) {
	collectedAt := time.Date(2026, time.September, 14, 6, 40, 0, 0, time.UTC)
	pages := make([]DailyPage, 0, DailyTimeSlots)
	for currentTime := range DailyTimeSlots {
		flightList := "[]"
		if currentTime == 0 {
			flightList = `[{"startSchemeTakeoffTime":"08:00","terminalSchemeLandinTime":"10:00","startRealTakeoffTime":"08:12","hbh":[{"flightNo":"CZ1001"}],"startStationThreecharcode":"SZX","terminalStationThreecharcode":"PEK","fltNormalStatus":"已起飞"}]`
		}
		if currentTime == 12 {
			flightList = `[{"startSchemeTakeoffTime":"08:00","terminalSchemeLandinTime":"10:00","hbh":[{"flightNo":"CZ1001"}],"startStationThreecharcode":"SZX","terminalStationThreecharcode":"PEK"},{"startSchemeTakeoffTime":"09:00","terminalSchemeLandinTime":"11:00","hbh":[{"flightNo":"ZH1002"}],"startStationThreecharcode":"SZX","terminalStationThreecharcode":"SHA"}]`
		}
		pages = append(pages, DailyPage{
			CurrentTime: currentTime,
			Payload: json.RawMessage(fmt.Sprintf(
				`{"flightList":%s,"type":"cn","flag":"D","currentDate":1,"currentTime":%d}`,
				flightList,
				currentTime,
			)),
		})
	}

	response, err := BuildDailySnapshot(DailyCollection{
		RunID:       "c91abb94-dc37-40aa-bc68-d362e5375690",
		ServiceDate: "2026-09-14",
		Direction:   "departure",
		CollectedAt: collectedAt,
		Pages:       pages,
	})
	if err != nil {
		t.Fatalf("BuildDailySnapshot returned error: %v", err)
	}

	if response.RunID != "c91abb94-dc37-40aa-bc68-d362e5375690" {
		t.Fatalf("unexpected run ID %q", response.RunID)
	}
	if response.ServiceDate != "2026-09-14" {
		t.Fatalf("unexpected service date %q", response.ServiceDate)
	}
	if !response.CollectedAt.Equal(collectedAt) {
		t.Fatalf("unexpected collected time %s", response.CollectedAt)
	}
	if response.Query.CurrentTime != "0-12" {
		t.Fatalf("expected merged currentTime range, got %q", response.Query.CurrentTime)
	}
	if response.Total != 2 {
		t.Fatalf("expected 2 merged flights, got %d", response.Total)
	}
	if response.Flights[0].ActualDeparture != "08:12" {
		t.Fatalf("expected richer duplicate to win, got actual departure %q", response.Flights[0].ActualDeparture)
	}
}

func TestBuildDailySnapshotRejectsIncompleteCollection(t *testing.T) {
	_, err := BuildDailySnapshot(DailyCollection{
		Direction: "departure",
		Pages: []DailyPage{{
			CurrentTime: 0,
			Payload:     json.RawMessage(`{"flightList":[]}`),
		}},
	})
	if err == nil {
		t.Fatal("expected incomplete collection error")
	}
}

func TestBuildDailySnapshotRejectsPayloadWithoutFlightList(t *testing.T) {
	pages := make([]DailyPage, 0, DailyTimeSlots)
	for currentTime := range DailyTimeSlots {
		payload := json.RawMessage(`{"flightList":[]}`)
		if currentTime == 4 {
			payload = json.RawMessage(`{"message":"blocked"}`)
		}
		pages = append(pages, DailyPage{CurrentTime: currentTime, Payload: payload})
	}

	_, err := BuildDailySnapshot(DailyCollection{
		Direction: "arrival",
		Pages:     pages,
	})
	if err == nil {
		t.Fatal("expected missing flightList error")
	}
}
