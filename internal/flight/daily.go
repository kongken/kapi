package flight

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"butterfly.orx.me/core/store/s3"
)

var ErrDailySnapshotNotFound = errors.New("daily snapshot not found")

var shanghaiLocation = mustLoadLocation("Asia/Shanghai")

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

func LoadDailySnapshot(ctx context.Context, airportCode string, direction string) ([]byte, error) {
	client := s3.GetClient(s3ConfigKey)
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

	return data, nil
}

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}
