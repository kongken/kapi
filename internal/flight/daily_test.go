package flight

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	redis "github.com/redis/go-redis/v9"
)

type testDailySnapshotCache struct {
	values   map[string]string
	getCalls int
	setCalls int
	lastKey  string
	lastTTL  time.Duration
	getErr   error
	setErr   error
}

func (c *testDailySnapshotCache) Get(_ context.Context, key string) (string, error) {
	c.getCalls++
	if c.getErr != nil {
		return "", c.getErr
	}
	value, ok := c.values[key]
	if !ok {
		return "", redis.Nil
	}
	return value, nil
}

func (c *testDailySnapshotCache) Set(_ context.Context, key string, value string, ttl time.Duration) error {
	c.setCalls++
	c.lastKey = key
	c.lastTTL = ttl
	if c.setErr != nil {
		return c.setErr
	}
	if c.values == nil {
		c.values = map[string]string{}
	}
	c.values[key] = value
	return nil
}

type testDailySnapshotS3Client struct {
	body []byte
	err  error
	gets int
}

func (c *testDailySnapshotS3Client) GetObject(_ context.Context, _ *awss3.GetObjectInput, _ ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	c.gets++
	if c.err != nil {
		return nil, c.err
	}
	return &awss3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(c.body))}, nil
}

func withDailySnapshotDeps(t *testing.T, cache dailySnapshotCache, s3Client s3ObjectGetter) {
	t.Helper()
	originalCache := getDailySnapshotCache
	originalS3 := getDailySnapshotS3Client
	getDailySnapshotCache = func() dailySnapshotCache { return cache }
	getDailySnapshotS3Client = func() s3ObjectGetter { return s3Client }
	t.Cleanup(func() {
		getDailySnapshotCache = originalCache
		getDailySnapshotS3Client = originalS3
	})
}

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

func TestDailySnapshotCacheKeyUsesShanghaiDate(t *testing.T) {
	now := time.Date(2026, 4, 28, 17, 30, 0, 0, time.UTC)
	key := DailySnapshotCacheKey("szx", "departure", now)

	want := "szx:flights:daily:szx:departure:2026-04-29"
	if key != want {
		t.Fatalf("expected key %q, got %q", want, key)
	}
}

func TestLoadDailySnapshotUsesRedisCacheFirst(t *testing.T) {
	cacheKey := DailySnapshotCacheKey("szx", "departure", time.Now())
	cache := &testDailySnapshotCache{values: map[string]string{cacheKey: `{"cached":true}`}}
	s3Client := &testDailySnapshotS3Client{}
	withDailySnapshotDeps(t, cache, s3Client)

	data, err := LoadDailySnapshot(context.Background(), "szx", "departure")
	if err != nil {
		t.Fatalf("LoadDailySnapshot returned error: %v", err)
	}
	if string(data) != `{"cached":true}` {
		t.Fatalf("expected cached payload, got %s", string(data))
	}
	if s3Client.gets != 0 {
		t.Fatalf("expected no S3 reads on cache hit, got %d", s3Client.gets)
	}
	if cache.setCalls != 0 {
		t.Fatalf("expected no cache writes on cache hit, got %d", cache.setCalls)
	}
}

func TestLoadDailySnapshotFallsBackToS3AndWarmsRedis(t *testing.T) {
	cache := &testDailySnapshotCache{}
	s3Client := &testDailySnapshotS3Client{body: []byte(`{"source":"szairport"}`)}
	withDailySnapshotDeps(t, cache, s3Client)

	data, err := LoadDailySnapshot(context.Background(), "szx", "arrival")
	if err != nil {
		t.Fatalf("LoadDailySnapshot returned error: %v", err)
	}
	if string(data) != `{"source":"szairport"}` {
		t.Fatalf("expected S3 payload, got %s", string(data))
	}
	if s3Client.gets != 1 {
		t.Fatalf("expected one S3 read, got %d", s3Client.gets)
	}
	if cache.setCalls != 1 {
		t.Fatalf("expected one cache warm-up write, got %d", cache.setCalls)
	}
	if cache.lastTTL != dailySnapshotCacheTTL {
		t.Fatalf("expected cache ttl %s, got %s", dailySnapshotCacheTTL, cache.lastTTL)
	}
	if cache.lastKey == "" || cache.values[cache.lastKey] != `{"source":"szairport"}` {
		t.Fatalf("expected warmed cache value, got %+v", cache.values)
	}
}

func TestLoadDailySnapshotReturnsNotFoundForMissingS3Object(t *testing.T) {
	cache := &testDailySnapshotCache{}
	s3Client := &testDailySnapshotS3Client{err: &s3types.NoSuchKey{}}
	withDailySnapshotDeps(t, cache, s3Client)

	_, err := LoadDailySnapshot(context.Background(), "szx", "arrival")
	if !errors.Is(err, ErrDailySnapshotNotFound) {
		t.Fatalf("expected ErrDailySnapshotNotFound, got %v", err)
	}
}
