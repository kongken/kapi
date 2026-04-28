package flight

import (
	"bytes"
	"context"
	"log/slog"
	"time"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"butterfly.orx.me/core/store/s3"
)

const s3ConfigKey = "flight"

type DailyFetcher interface {
	FetchDailyFlights(ctx context.Context, direction string) ([]byte, error)
}

type airport struct {
	code    string
	fetcher DailyFetcher
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
func (s *Syncer) Register(code string, fetcher DailyFetcher) {
	s.airports = append(s.airports, airport{code: code, fetcher: fetcher})
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

func (s *Syncer) syncDailyAll(ctx context.Context) {
	for _, ap := range s.airports {
		for _, direction := range []string{"departure", "arrival"} {
			data, err := ap.fetcher.FetchDailyFlights(ctx, direction)
			if err != nil {
				slog.Error("failed to fetch daily flights",
					"airport", ap.code, "direction", direction, "error", err)
				continue
			}
			s.saveDailySnapshot(ctx, ap.code, direction, data)
		}
	}
}

func (s *Syncer) saveDailySnapshot(ctx context.Context, airportCode string, direction string, data []byte) {
	now := time.Now()
	storeDailySnapshotInCache(ctx, getDailySnapshotCache(), DailySnapshotCacheKey(airportCode, direction, now), data)
	s.putObject(ctx, DailySnapshotLatestKey(airportCode, direction, now), data)
	s.putObject(ctx, DailySnapshotVersionedKey(airportCode, direction, now), data)
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
