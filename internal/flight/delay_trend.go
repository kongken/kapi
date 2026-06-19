package flight

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kongken/kapi/internal/szx"
)

type DelayTrendResponse struct {
	Source     string             `json:"source"`
	Airport    string             `json:"airport"`
	Resource   string             `json:"resource"`
	Directions []string           `json:"directions"`
	Total      DelayTrendCounters `json:"total"`
	Buckets    []DelayTrendBucket `json:"buckets"`
}

type DelayTrendBucket struct {
	Hour       int                `json:"hour"`
	TimeRange  string             `json:"timeRange"`
	Directions []DirectionBucket  `json:"directions"`
	Total      DelayTrendCounters `json:"total"`
}

type DirectionBucket struct {
	Direction string             `json:"direction"`
	Counters  DelayTrendCounters `json:"counters"`
}

type DelayTrendCounters struct {
	Flights    int     `json:"flights"`
	Delayed    int     `json:"delayed"`
	Cancelled  int     `json:"cancelled"`
	Diverted   int     `json:"diverted"`
	Affected   int     `json:"affected"`
	DelayRatio float64 `json:"delayRatio"`
}

type delayTrendAccumulator struct {
	Flights   int
	Delayed   int
	Cancelled int
	Diverted  int
}

var ErrDelayTrendNoSnapshots = errors.New("delay trend requires at least one daily snapshot")

func BuildSZXDelayTrend(snapshots map[string][]byte) (DelayTrendResponse, error) {
	if len(snapshots) == 0 {
		return DelayTrendResponse{}, ErrDelayTrendNoSnapshots
	}

	directions := orderedDirections(snapshots)
	if len(directions) == 0 {
		return DelayTrendResponse{}, ErrDelayTrendNoSnapshots
	}

	buckets := make(map[int]map[string]*delayTrendAccumulator)
	totalAccumulator := &delayTrendAccumulator{}

	for _, direction := range directions {
		var response szx.Response
		if err := json.Unmarshal(snapshots[direction], &response); err != nil {
			return DelayTrendResponse{}, fmt.Errorf("decode %s daily snapshot: %w", direction, err)
		}

		for _, item := range response.Flights {
			hour, ok := scheduledHour(direction, item)
			if !ok {
				continue
			}
			if _, ok := buckets[hour]; !ok {
				buckets[hour] = make(map[string]*delayTrendAccumulator)
			}
			if _, ok := buckets[hour][direction]; !ok {
				buckets[hour][direction] = &delayTrendAccumulator{}
			}

			classifyDelayTrendFlight(item, buckets[hour][direction])
			classifyDelayTrendFlight(item, totalAccumulator)
		}
	}

	hours := make([]int, 0, len(buckets))
	for hour := range buckets {
		hours = append(hours, hour)
	}
	sort.Ints(hours)

	responseBuckets := make([]DelayTrendBucket, 0, len(hours))
	for _, hour := range hours {
		directionBuckets := make([]DirectionBucket, 0, len(directions))
		bucketTotal := &delayTrendAccumulator{}
		for _, direction := range directions {
			accumulator := buckets[hour][direction]
			if accumulator == nil {
				accumulator = &delayTrendAccumulator{}
			}
			directionBuckets = append(directionBuckets, DirectionBucket{
				Direction: direction,
				Counters:  accumulator.toCounters(),
			})
			bucketTotal.add(accumulator)
		}

		responseBuckets = append(responseBuckets, DelayTrendBucket{
			Hour:       hour,
			TimeRange:  fmt.Sprintf("%02d:00-%02d:59", hour, hour),
			Directions: directionBuckets,
			Total:      bucketTotal.toCounters(),
		})
	}

	return DelayTrendResponse{
		Source:     "szairport",
		Airport:    "szx",
		Resource:   "delay_trend",
		Directions: directions,
		Total:      totalAccumulator.toCounters(),
		Buckets:    responseBuckets,
	}, nil
}

func orderedDirections(snapshots map[string][]byte) []string {
	directions := make([]string, 0, len(snapshots))
	for _, direction := range []string{"departure", "arrival"} {
		if _, ok := snapshots[direction]; ok {
			directions = append(directions, direction)
		}
	}
	for direction := range snapshots {
		if direction != "departure" && direction != "arrival" {
			directions = append(directions, direction)
		}
	}
	return directions
}

func scheduledHour(direction string, item szx.Flight) (int, bool) {
	value := item.PlannedDeparture
	if direction == "arrival" {
		value = item.PlannedArrival
	}

	if len(value) < len("15:04") || value == "--:--" {
		return 0, false
	}
	hour, err := strconv.Atoi(value[:2])
	if err != nil || hour < 0 || hour > 23 {
		return 0, false
	}
	return hour, true
}

func classifyDelayTrendFlight(item szx.Flight, accumulator *delayTrendAccumulator) {
	accumulator.Flights++

	status := strings.TrimSpace(item.StatusText + " " + item.StatusCode)
	if strings.Contains(status, "取消") || strings.Contains(strings.ToLower(status), "cancel") {
		accumulator.Cancelled++
		return
	}
	if strings.Contains(status, "备降") || strings.Contains(strings.ToLower(status), "divert") {
		accumulator.Diverted++
		return
	}
	if strings.Contains(status, "延误") || strings.Contains(strings.ToLower(status), "delay") {
		accumulator.Delayed++
	}
}

func (a *delayTrendAccumulator) add(other *delayTrendAccumulator) {
	a.Flights += other.Flights
	a.Delayed += other.Delayed
	a.Cancelled += other.Cancelled
	a.Diverted += other.Diverted
}

func (a *delayTrendAccumulator) toCounters() DelayTrendCounters {
	affected := a.Delayed + a.Cancelled + a.Diverted
	ratio := 0.0
	if a.Flights > 0 {
		ratio = float64(affected) / float64(a.Flights)
	}
	return DelayTrendCounters{
		Flights:    a.Flights,
		Delayed:    a.Delayed,
		Cancelled:  a.Cancelled,
		Diverted:   a.Diverted,
		Affected:   affected,
		DelayRatio: ratio,
	}
}
