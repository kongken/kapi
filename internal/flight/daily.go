package flight

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	bredis "butterfly.orx.me/core/store/redis"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"butterfly.orx.me/core/store/s3"
	redis "github.com/redis/go-redis/v9"
)

var ErrDailySnapshotNotFound = errors.New("daily snapshot not found")

var shanghaiLocation = mustLoadLocation("Asia/Shanghai")

const dailySnapshotRedisKey = "szx:flights:daily:%s:%s:%s"
const dailySnapshotCacheTTL = 35 * time.Minute

// DailySnapshot is a normalized snapshot ready for durable storage.
type DailySnapshot struct {
	AirportCode string
	Direction   string
	ServiceDate string
	RunID       string
	CollectedAt time.Time
	Data        []byte
}

type dailySnapshotCache interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
}

type s3ObjectGetter interface {
	GetObject(ctx context.Context, params *awss3.GetObjectInput, optFns ...func(*awss3.Options)) (*awss3.GetObjectOutput, error)
}

type s3ObjectPutter interface {
	PutObject(ctx context.Context, params *awss3.PutObjectInput, optFns ...func(*awss3.Options)) (*awss3.PutObjectOutput, error)
}

var getDailySnapshotCache = func() dailySnapshotCache {
	return redisClientAdapter{client: bredis.GetClient("default")}
}

var getDailySnapshotS3Client = func() s3ObjectGetter {
	return s3.GetClient(s3ConfigKey)
}

var getDailySnapshotS3Putter = func() s3ObjectPutter {
	return s3.GetClient(s3ConfigKey)
}

func DailySnapshotLatestKey(airportCode string, direction string, now time.Time) string {
	date := now.In(shanghaiLocation).Format(time.DateOnly)
	return dailySnapshotLatestKeyForDate(airportCode, direction, date)
}

func DailySnapshotVersionedKey(airportCode string, direction string, now time.Time) string {
	localNow := now.In(shanghaiLocation)
	return fmt.Sprintf("flights/%s/%s/daily/%s/%d-%d.json",
		airportCode,
		direction,
		localNow.Format(time.DateOnly),
		localNow.Hour(),
		localNow.Minute(),
	)
}

func DailySnapshotCacheKey(airportCode string, direction string, now time.Time) string {
	date := now.In(shanghaiLocation).Format(time.DateOnly)
	return dailySnapshotCacheKeyForDate(airportCode, direction, date)
}

func dailySnapshotLatestKeyForDate(airportCode string, direction string, serviceDate string) string {
	return fmt.Sprintf("flights/%s/%s/daily/%s/latest.json", airportCode, direction, serviceDate)
}

func dailySnapshotVersionedKey(snapshot DailySnapshot) string {
	if snapshot.RunID == "" {
		return DailySnapshotVersionedKey(snapshot.AirportCode, snapshot.Direction, snapshot.CollectedAt)
	}
	localCollectedAt := snapshot.CollectedAt.In(shanghaiLocation)
	return fmt.Sprintf("flights/%s/%s/daily/%s/%02d-%02d-%02d-%s.json",
		snapshot.AirportCode,
		snapshot.Direction,
		snapshot.ServiceDate,
		localCollectedAt.Hour(),
		localCollectedAt.Minute(),
		localCollectedAt.Second(),
		snapshot.RunID,
	)
}

func dailySnapshotCacheKeyForDate(airportCode string, direction string, serviceDate string) string {
	return fmt.Sprintf(dailySnapshotRedisKey, airportCode, direction, serviceDate)
}

func LoadDailySnapshot(ctx context.Context, airportCode string, direction string) ([]byte, error) {
	serviceDate := time.Now().In(shanghaiLocation).Format(time.DateOnly)
	return LoadDailySnapshotForDate(ctx, airportCode, direction, serviceDate)
}

// LoadDailySnapshotForDate loads a specific Shanghai service date from Redis,
// falling back to the durable S3 snapshot.
func LoadDailySnapshotForDate(ctx context.Context, airportCode string, direction string, serviceDate string) ([]byte, error) {
	cacheKey := dailySnapshotCacheKeyForDate(airportCode, direction, serviceDate)
	if data, ok := loadDailySnapshotFromCache(ctx, getDailySnapshotCache(), cacheKey); ok {
		return data, nil
	}

	client := getDailySnapshotS3Client()
	if client == nil {
		return nil, errors.New("s3 client not configured")
	}

	bucket := s3.GetBucket(s3ConfigKey)
	key := dailySnapshotLatestKeyForDate(airportCode, direction, serviceDate)
	resp, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		var notFound *s3types.NoSuchKey
		if errors.As(err, &notFound) {
			return nil, ErrDailySnapshotNotFound
		}
		return nil, fmt.Errorf("get daily snapshot %s: %w", key, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read daily snapshot %s: %w", key, err)
	}

	if err := storeDailySnapshotInCache(ctx, getDailySnapshotCache(), cacheKey, data); err != nil {
		slog.Warn("failed to warm daily flights cache", "key", cacheKey, "error", err)
	}

	return data, nil
}

// SaveDailySnapshot writes a versioned object before publishing latest.json.
// Redis is a best-effort cache and is updated only after durable writes succeed.
func SaveDailySnapshot(ctx context.Context, snapshot DailySnapshot) error {
	if snapshot.AirportCode == "" {
		return errors.New("airport code is required")
	}
	if snapshot.Direction == "" {
		return errors.New("direction is required")
	}
	if len(snapshot.Data) == 0 {
		return errors.New("snapshot data is required")
	}
	if snapshot.CollectedAt.IsZero() {
		snapshot.CollectedAt = time.Now()
	}
	if snapshot.ServiceDate == "" {
		snapshot.ServiceDate = snapshot.CollectedAt.In(shanghaiLocation).Format(time.DateOnly)
	}

	client := getDailySnapshotS3Putter()
	if client == nil {
		return errors.New("s3 client not configured")
	}
	bucket := s3.GetBucket(s3ConfigKey)
	metadata := map[string]string{
		"collected-at": snapshot.CollectedAt.UTC().Format(time.RFC3339Nano),
	}
	if snapshot.RunID != "" {
		metadata["run-id"] = snapshot.RunID
	}

	versionedKey := dailySnapshotVersionedKey(snapshot)
	if err := putDailySnapshotObject(ctx, client, bucket, versionedKey, snapshot.Data, metadata); err != nil {
		return err
	}

	latestKey := dailySnapshotLatestKeyForDate(snapshot.AirportCode, snapshot.Direction, snapshot.ServiceDate)
	if err := putDailySnapshotObject(ctx, client, bucket, latestKey, snapshot.Data, metadata); err != nil {
		return err
	}

	cacheKey := dailySnapshotCacheKeyForDate(snapshot.AirportCode, snapshot.Direction, snapshot.ServiceDate)
	if err := storeDailySnapshotInCache(ctx, getDailySnapshotCache(), cacheKey, snapshot.Data); err != nil {
		slog.Warn("failed to store daily flights cache", "key", cacheKey, "error", err)
	}

	return nil
}

func putDailySnapshotObject(
	ctx context.Context,
	client s3ObjectPutter,
	bucket string,
	key string,
	data []byte,
	metadata map[string]string,
) error {
	contentType := "application/json"
	_, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:      &bucket,
		Key:         &key,
		Body:        bytes.NewReader(data),
		ContentType: &contentType,
		Metadata:    metadata,
	})
	if err != nil {
		return fmt.Errorf("put daily snapshot %s: %w", key, err)
	}

	slog.Info("saved to s3", "key", key)
	return nil
}

func loadDailySnapshotFromCache(ctx context.Context, client dailySnapshotCache, key string) ([]byte, bool) {
	if client == nil {
		return nil, false
	}

	value, err := client.Get(ctx, key)
	if err == nil {
		slog.Info("daily flights cache hit", "key", key)
		return []byte(value), true
	}
	if errors.Is(err, redis.Nil) {
		slog.Info("daily flights cache miss", "key", key)
		return nil, false
	}

	slog.Warn("failed to load daily flights cache", "key", key, "error", err)
	return nil, false
}

func storeDailySnapshotInCache(ctx context.Context, client dailySnapshotCache, key string, data []byte) error {
	if client == nil {
		return nil
	}

	if err := client.Set(ctx, key, string(data), dailySnapshotCacheTTL); err != nil {
		return err
	}

	slog.Info("stored daily flights cache", "key", key, "ttl", dailySnapshotCacheTTL)
	return nil
}

type redisClientAdapter struct {
	client redis.UniversalClient
}

func (c redisClientAdapter) Get(ctx context.Context, key string) (string, error) {
	if c.client == nil {
		return "", redis.Nil
	}
	return c.client.Get(ctx, key).Result()
}

func (c redisClientAdapter) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	if c.client == nil {
		return nil
	}
	return c.client.Set(ctx, key, value, ttl).Err()
}

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}
