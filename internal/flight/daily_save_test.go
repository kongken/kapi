package flight

import (
	"context"
	"errors"
	"testing"
	"time"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

type testDailySnapshotS3Putter struct {
	keys      []string
	metadata  []map[string]string
	failOnPut int
}

func (p *testDailySnapshotS3Putter) PutObject(
	_ context.Context,
	input *awss3.PutObjectInput,
	_ ...func(*awss3.Options),
) (*awss3.PutObjectOutput, error) {
	p.keys = append(p.keys, *input.Key)
	p.metadata = append(p.metadata, input.Metadata)
	if p.failOnPut == len(p.keys) {
		return nil, errors.New("put failed")
	}
	return &awss3.PutObjectOutput{}, nil
}

func TestSaveDailySnapshotPublishesDurableObjectsBeforeCache(t *testing.T) {
	cache := &testDailySnapshotCache{}
	putter := &testDailySnapshotS3Putter{}
	originalCache := getDailySnapshotCache
	originalPutter := getDailySnapshotS3Putter
	getDailySnapshotCache = func() dailySnapshotCache { return cache }
	getDailySnapshotS3Putter = func() s3ObjectPutter { return putter }
	t.Cleanup(func() {
		getDailySnapshotCache = originalCache
		getDailySnapshotS3Putter = originalPutter
	})

	collectedAt := time.Date(2026, time.September, 14, 6, 40, 0, 0, time.UTC)
	err := SaveDailySnapshot(t.Context(), DailySnapshot{
		AirportCode: "szx",
		Direction:   "departure",
		ServiceDate: "2026-09-14",
		RunID:       "c91abb94-dc37-40aa-bc68-d362e5375690",
		CollectedAt: collectedAt,
		Data:        []byte(`{"total":1}`),
	})
	if err != nil {
		t.Fatalf("SaveDailySnapshot returned error: %v", err)
	}

	wantVersioned := "flights/szx/departure/daily/2026-09-14/14-40-00-c91abb94-dc37-40aa-bc68-d362e5375690.json"
	wantLatest := "flights/szx/departure/daily/2026-09-14/latest.json"
	if len(putter.keys) != 2 || putter.keys[0] != wantVersioned || putter.keys[1] != wantLatest {
		t.Fatalf("unexpected S3 writes: %v", putter.keys)
	}
	if putter.metadata[0]["run-id"] != "c91abb94-dc37-40aa-bc68-d362e5375690" {
		t.Fatalf("missing run metadata: %v", putter.metadata[0])
	}
	if cache.setCalls != 1 {
		t.Fatalf("expected cache update after durable writes, got %d", cache.setCalls)
	}
	wantCacheKey := "szx:flights:daily:szx:departure:2026-09-14"
	if cache.lastKey != wantCacheKey {
		t.Fatalf("expected cache key %q, got %q", wantCacheKey, cache.lastKey)
	}
}

func TestSaveDailySnapshotDoesNotPublishCacheWhenS3Fails(t *testing.T) {
	cache := &testDailySnapshotCache{}
	putter := &testDailySnapshotS3Putter{failOnPut: 2}
	originalCache := getDailySnapshotCache
	originalPutter := getDailySnapshotS3Putter
	getDailySnapshotCache = func() dailySnapshotCache { return cache }
	getDailySnapshotS3Putter = func() s3ObjectPutter { return putter }
	t.Cleanup(func() {
		getDailySnapshotCache = originalCache
		getDailySnapshotS3Putter = originalPutter
	})

	err := SaveDailySnapshot(t.Context(), DailySnapshot{
		AirportCode: "szx",
		Direction:   "arrival",
		ServiceDate: "2026-09-14",
		RunID:       "d0cd78ec-fd1c-4a27-b7df-c20ee2bb9646",
		CollectedAt: time.Date(2026, time.September, 14, 6, 40, 0, 0, time.UTC),
		Data:        []byte(`{"total":1}`),
	})
	if err == nil {
		t.Fatal("expected latest object write error")
	}
	if cache.setCalls != 0 {
		t.Fatalf("expected no cache publication, got %d writes", cache.setCalls)
	}
}
