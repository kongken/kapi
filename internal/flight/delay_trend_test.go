package flight

import (
	"errors"
	"testing"
)

func TestBuildSZXDelayTrend(t *testing.T) {
	response, err := BuildSZXDelayTrend(map[string][]byte{
		"departure": []byte(`{"source":"szairport","direction":"departure","total":3,"flights":[{"plannedDepartureTime":"08:10","statusText":"延误"},{"plannedDepartureTime":"08:30","statusText":"取消"},{"plannedDepartureTime":"09:00","statusText":"已起飞"}]}`),
		"arrival":   []byte(`{"source":"szairport","direction":"arrival","total":2,"flights":[{"plannedArrivalTime":"08:20","statusText":"备降"},{"plannedArrivalTime":"10:05","statusText":"LANDED"}]}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if response.Source != "szairport" || response.Airport != "szx" || response.Resource != "delay_trend" {
		t.Fatalf("unexpected response metadata: %+v", response)
	}
	if response.Total.Flights != 5 || response.Total.Affected != 3 || response.Total.Delayed != 1 || response.Total.Cancelled != 1 || response.Total.Diverted != 1 {
		t.Fatalf("unexpected total counters: %+v", response.Total)
	}
	if response.Total.DelayRatio != 0.6 {
		t.Fatalf("expected delay ratio 0.6, got %v", response.Total.DelayRatio)
	}
	if len(response.Buckets) != 3 {
		t.Fatalf("expected 3 buckets, got %d", len(response.Buckets))
	}

	first := response.Buckets[0]
	if first.Hour != 8 || first.TimeRange != "08:00-08:59" {
		t.Fatalf("unexpected first bucket: %+v", first)
	}
	if first.Total.Flights != 3 || first.Total.Affected != 3 {
		t.Fatalf("unexpected first bucket total: %+v", first.Total)
	}
	if first.Directions[0].Direction != "departure" || first.Directions[0].Counters.Affected != 2 {
		t.Fatalf("unexpected departure bucket: %+v", first.Directions[0])
	}
	if first.Directions[1].Direction != "arrival" || first.Directions[1].Counters.Diverted != 1 {
		t.Fatalf("unexpected arrival bucket: %+v", first.Directions[1])
	}
}

func TestBuildSZXDelayTrendRequiresSnapshot(t *testing.T) {
	_, err := BuildSZXDelayTrend(nil)
	if !errors.Is(err, ErrDelayTrendNoSnapshots) {
		t.Fatalf("expected ErrDelayTrendNoSnapshots, got %v", err)
	}
}

func TestBuildSZXDelayTrendClassifiesStatusCodes(t *testing.T) {
	response, err := BuildSZXDelayTrend(map[string][]byte{
		"departure": []byte(`{"source":"szairport","direction":"departure","total":4,"flights":[{"plannedDepartureTime":"08:10","statusCode":"DELAYED"},{"plannedDepartureTime":"08:20","statusCode":"C"},{"plannedDepartureTime":"08:30","statusCode":"DIVERTED"},{"plannedDepartureTime":"08:40","statusCode":"D","statusText":"DEPARTED"}]}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if response.Total.Flights != 4 {
		t.Fatalf("expected 4 flights, got %d", response.Total.Flights)
	}
	if response.Total.Delayed != 1 || response.Total.Cancelled != 1 || response.Total.Diverted != 1 || response.Total.Affected != 3 {
		t.Fatalf("unexpected total counters: %+v", response.Total)
	}
}
