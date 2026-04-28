package flight

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"butterfly.orx.me/core/store/s3"
)

const s3ConfigKey = "flight"

// LandedFlight represents a single flight that has landed.
type LandedFlight struct {
	FlightNumbers []string
	Date          string // YYYY-MM-DD
	Data          []byte // JSON of the individual flight
}

// FetchResult contains the full response and any landed flights extracted from it.
type FetchResult struct {
	Data          []byte         // Full response JSON
	LandedFlights []LandedFlight // Individual landed flights
}

// Fetcher fetches flight data for an airport.
// Each airport implements this interface.
type Fetcher interface {
	FetchFlights(ctx context.Context, direction string) (*FetchResult, error)
}

type DailyFetcher interface {
	FetchDailyFlights(ctx context.Context, direction string) ([]byte, error)
}

type airport struct {
	code    string
	fetcher Fetcher
}

// Syncer periodically fetches flight data from registered airports and persists to S3.
type Syncer struct {
	airports []airport
}

func NewSyncer() *Syncer {
	return &Syncer{}
}

// Register adds an airport to the sync loop.
// code is the IATA airport code (e.g. "szx", "pek"), used as the S3 key prefix.
func (s *Syncer) Register(code string, fetcher Fetcher) {
	s.airports = append(s.airports, airport{code: code, fetcher: fetcher})
}

// StartSync runs the sync loop. It fetches immediately on start, then every interval.
// Blocks until ctx is cancelled.
func (s *Syncer) StartSync(ctx context.Context, interval time.Duration) {
	slog.Info("starting flight sync", "interval", interval, "airports", len(s.airports))

	s.syncAll(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("stopping flight sync")
			return
		case <-ticker.C:
			s.syncAll(ctx)
		}
	}
}

func (s *Syncer) StartDailySync(ctx context.Context, interval time.Duration) {
	slog.Info("starting daily flight sync", "interval", interval, "airports", len(s.airports))

	s.syncDailyAll(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("stopping daily flight sync")
			return
		case <-ticker.C:
			s.syncDailyAll(ctx)
		}
	}
}

func (s *Syncer) syncAll(ctx context.Context) {
	for _, ap := range s.airports {
		for _, direction := range []string{"departure", "arrival"} {
			result, err := ap.fetcher.FetchFlights(ctx, direction)
			if err != nil {
				slog.Error("failed to fetch flights",
					"airport", ap.code, "direction", direction, "error", err)
				continue
			}
			s.saveSnapshot(ctx, ap.code, direction, result.Data)
			s.saveLandedFlights(ctx, result.LandedFlights)
		}
	}
}

func (s *Syncer) syncDailyAll(ctx context.Context) {
	for _, ap := range s.airports {
		dailyFetcher, ok := ap.fetcher.(DailyFetcher)
		if !ok {
			continue
		}

		for _, direction := range []string{"departure", "arrival"} {
			data, err := dailyFetcher.FetchDailyFlights(ctx, direction)
			if err != nil {
				slog.Error("failed to fetch daily flights",
					"airport", ap.code, "direction", direction, "error", err)
				continue
			}
			s.saveDailySnapshot(ctx, ap.code, direction, data)
		}
	}
}

func (s *Syncer) saveSnapshot(ctx context.Context, airportCode, direction string, data []byte) {
	now := time.Now().UTC()
	key := fmt.Sprintf("flights/%s/%s/%s/%s.json",
		airportCode, direction, now.Format("2006-01-02"), now.Format("15-04-05"))
	s.putObject(ctx, key, data)
}

func (s *Syncer) saveDailySnapshot(ctx context.Context, airportCode string, direction string, data []byte) {
	key := DailySnapshotKey(airportCode, direction, time.Now())
	s.putObject(ctx, key, data)
}

func (s *Syncer) saveLandedFlights(ctx context.Context, flights []LandedFlight) {
	for _, f := range flights {
		for _, fn := range f.FlightNumbers {
			key := fmt.Sprintf("flights/%s/%s/%s/%s.json", fn, f.Date[:4], f.Date[5:7], f.Date[8:10])
			s.putObject(ctx, key, f.Data)
		}
	}
}

func (s *Syncer) putObject(ctx context.Context, key string, data []byte) {
	client := s3.GetClient(s3ConfigKey)
	bucket := s3.GetBucket(s3ConfigKey)
	contentType := "application/json"

	_, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:      &bucket,
		Key:         &key,
		Body:        bytes.NewReader(data),
		ContentType: &contentType,
	})
	if err != nil {
		slog.Error("failed to save to s3", "key", key, "error", err)
		return
	}

	slog.Info("saved to s3", "key", key)
}
