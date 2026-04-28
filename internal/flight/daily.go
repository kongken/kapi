package flight

import (
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

type dailySnapshotCache interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
}

type s3ObjectGetter interface {
	GetObject(ctx context.Context, params *awss3.GetObjectInput, optFns ...func(*awss3.Options)) (*awss3.GetObjectOutput, error)
}

var getDailySnapshotCache = func() dailySnapshotCache {
	return redisClientAdapter{client: bredis.GetClient("default")}
}

var getDailySnapshotS3Client = func() s3ObjectGetter {
	return s3.GetClient(s3ConfigKey)
}

func DailySnapshotLatestKey(airportCode string, direction string, now time.Time) string {
	date := now.In(shanghaiLocation).Format("2006-01-02")
	return fmt.Sprintf("flights/%s/%s/daily/%s/latest.json", airportCode, direction, date)
}

func DailySnapshotVersionedKey(airportCode string, direction string, now time.Time) string {
	localNow := now.In(shanghaiLocation)
	return fmt.Sprintf("flights/%s/%s/daily/%s/%d-%d.json",
		airportCode,
		direction,
		localNow.Format("2006-01-02"),
		localNow.Hour(),
		localNow.Minute(),
	)
}

func DailySnapshotCacheKey(airportCode string, direction string, now time.Time) string {
	date := now.In(shanghaiLocation).Format("2006-01-02")
	return fmt.Sprintf(dailySnapshotRedisKey, airportCode, direction, date)
}

func LoadDailySnapshot(ctx context.Context, airportCode string, direction string) ([]byte, error) {
	cacheKey := DailySnapshotCacheKey(airportCode, direction, time.Now())
	if data, ok := loadDailySnapshotFromCache(ctx, getDailySnapshotCache(), cacheKey); ok {
		return data, nil
	}

	client := getDailySnapshotS3Client()
	if client == nil {
		return nil, errors.New("s3 client not configured")
	}

	bucket := s3.GetBucket(s3ConfigKey)
	key := DailySnapshotLatestKey(airportCode, direction, time.Now())
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

	storeDailySnapshotInCache(ctx, getDailySnapshotCache(), cacheKey, data)

	return data, nil
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

func storeDailySnapshotInCache(ctx context.Context, client dailySnapshotCache, key string, data []byte) {
	if client == nil {
		return
	}

	if err := client.Set(ctx, key, string(data), dailySnapshotCacheTTL); err != nil {
		slog.Warn("failed to store daily flights cache", "key", key, "error", err)
		return
	}

	slog.Info("stored daily flights cache", "key", key, "ttl", dailySnapshotCacheTTL)
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
