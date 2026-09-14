package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kongken/kapi/internal/flight"
	"github.com/kongken/kapi/internal/szx"
)

func TestSZXFlightIngestorAcceptsCompleteCollection(t *testing.T) {
	now := time.Date(2026, time.September, 14, 6, 45, 0, 0, time.UTC)
	var saved flight.DailySnapshot
	ingestor := NewSZXFlightIngestor(
		SnapshotLoaderFunc(func(context.Context, string, string, string) ([]byte, error) {
			return nil, flight.ErrDailySnapshotNotFound
		}),
		SnapshotWriterFunc(func(_ context.Context, snapshot flight.DailySnapshot) error {
			saved = snapshot
			return nil
		}),
	)
	ingestor.now = func() time.Time { return now }

	batch := validSZXFlightBatch(now.Add(-time.Minute))
	receipt, err := ingestor.Submit(t.Context(), batch)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}

	if receipt.Duplicate {
		t.Fatal("expected a newly accepted collection")
	}
	if receipt.Total != 1 {
		t.Fatalf("expected one merged flight, got %d", receipt.Total)
	}
	if saved.RunID != batch.RunID || saved.Direction != batch.Direction || saved.ServiceDate != batch.ServiceDate {
		t.Fatalf("unexpected saved snapshot metadata: %+v", saved)
	}

	var response szx.Response
	if err := json.Unmarshal(saved.Data, &response); err != nil {
		t.Fatalf("decode saved snapshot: %v", err)
	}
	if response.RunID != batch.RunID || !response.CollectedAt.Equal(batch.CollectedAt) {
		t.Fatalf("unexpected response metadata: %+v", response)
	}
}

func TestSZXFlightIngestorTreatsCurrentRunAsDuplicate(t *testing.T) {
	now := time.Date(2026, time.September, 14, 6, 45, 0, 0, time.UTC)
	batch := validSZXFlightBatch(now.Add(-time.Minute))
	existing, err := json.Marshal(szx.Response{
		RunID:       batch.RunID,
		ServiceDate: batch.ServiceDate,
		CollectedAt: batch.CollectedAt,
		Total:       1,
	})
	if err != nil {
		t.Fatalf("marshal existing snapshot: %v", err)
	}

	writes := 0
	ingestor := NewSZXFlightIngestor(
		SnapshotLoaderFunc(func(context.Context, string, string, string) ([]byte, error) {
			return existing, nil
		}),
		SnapshotWriterFunc(func(context.Context, flight.DailySnapshot) error {
			writes++
			return nil
		}),
	)
	ingestor.now = func() time.Time { return now }

	receipt, err := ingestor.Submit(t.Context(), batch)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if !receipt.Duplicate {
		t.Fatal("expected duplicate receipt")
	}
	if writes != 0 {
		t.Fatalf("expected no duplicate write, got %d", writes)
	}
}

func TestSZXFlightIngestorRejectsOlderCollection(t *testing.T) {
	now := time.Date(2026, time.September, 14, 6, 45, 0, 0, time.UTC)
	batch := validSZXFlightBatch(now.Add(-2 * time.Minute))
	existing, err := json.Marshal(szx.Response{
		RunID:       "f54505fd-eb72-4f58-a46e-ec928487ce30",
		ServiceDate: batch.ServiceDate,
		CollectedAt: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("marshal existing snapshot: %v", err)
	}

	ingestor := NewSZXFlightIngestor(
		SnapshotLoaderFunc(func(context.Context, string, string, string) ([]byte, error) {
			return existing, nil
		}),
		SnapshotWriterFunc(func(context.Context, flight.DailySnapshot) error {
			t.Fatal("stale collection must not be written")
			return nil
		}),
	)
	ingestor.now = func() time.Time { return now }

	_, err = ingestor.Submit(t.Context(), batch)
	if !errors.Is(err, ErrStaleSZXFlightBatch) {
		t.Fatalf("expected ErrStaleSZXFlightBatch, got %v", err)
	}
}

func TestSZXFlightIngestorRejectsCollectionDateMismatch(t *testing.T) {
	now := time.Date(2026, time.September, 14, 6, 45, 0, 0, time.UTC)
	batch := validSZXFlightBatch(now)
	batch.ServiceDate = "2026-09-13"

	ingestor := NewSZXFlightIngestor(
		SnapshotLoaderFunc(func(context.Context, string, string, string) ([]byte, error) {
			t.Fatal("invalid collection must not be loaded")
			return nil, nil
		}),
		SnapshotWriterFunc(func(context.Context, flight.DailySnapshot) error {
			t.Fatal("invalid collection must not be written")
			return nil
		}),
	)
	ingestor.now = func() time.Time { return now }

	_, err := ingestor.Submit(t.Context(), batch)
	if !errors.Is(err, ErrInvalidSZXFlightBatch) {
		t.Fatalf("expected ErrInvalidSZXFlightBatch, got %v", err)
	}
}

func validSZXFlightBatch(collectedAt time.Time) SZXFlightBatch {
	pages := make([]szx.DailyPage, 0, szx.DailyTimeSlots)
	for currentTime := range szx.DailyTimeSlots {
		flightList := "[]"
		if currentTime == 0 {
			flightList = `[{"startSchemeTakeoffTime":"08:00","terminalSchemeLandinTime":"10:00","hbh":[{"flightNo":"CZ1001"}],"startStationThreecharcode":"SZX","terminalStationThreecharcode":"PEK"}]`
		}
		pages = append(pages, szx.DailyPage{
			CurrentTime: currentTime,
			Payload: json.RawMessage(fmt.Sprintf(
				`{"flightList":%s,"type":"cn","flag":"D","currentDate":1,"currentTime":%d}`,
				flightList,
				currentTime,
			)),
		})
	}

	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}
	return SZXFlightBatch{
		SchemaVersion: 1,
		RunID:         "c91abb94-dc37-40aa-bc68-d362e5375690",
		Source:        "szairport",
		Airport:       "szx",
		Direction:     "departure",
		ServiceDate:   collectedAt.In(shanghai).Format(time.DateOnly),
		CollectedAt:   collectedAt,
		Pages:         pages,
	}
}
