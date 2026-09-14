package szx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

const DailyTimeSlots = 13

const maxDailyPageBytes = 2 << 20

// DailyPage is one raw response collected from the SZX upstream for a
// currentTime slot.
type DailyPage struct {
	CurrentTime int             `json:"currentTime"`
	Payload     json.RawMessage `json:"payload"`
}

// DailyCollection is a complete direction-specific SZX collection run.
type DailyCollection struct {
	RunID       string      `json:"runId"`
	ServiceDate string      `json:"serviceDate"`
	Direction   string      `json:"direction"`
	CollectedAt time.Time   `json:"collectedAt"`
	Pages       []DailyPage `json:"pages"`
}

// BuildDailySnapshot validates and normalizes a complete collection without
// performing network or storage I/O.
func BuildDailySnapshot(collection DailyCollection) (Response, error) {
	flag, err := directionFlag(collection.Direction)
	if err != nil {
		return Response{}, err
	}
	if len(collection.Pages) != DailyTimeSlots {
		return Response{}, fmt.Errorf("collection must contain exactly %d pages", DailyTimeSlots)
	}

	responses := make([]Response, DailyTimeSlots)
	seen := make([]bool, DailyTimeSlots)
	for _, page := range collection.Pages {
		if page.CurrentTime < 0 || page.CurrentTime >= DailyTimeSlots {
			return Response{}, fmt.Errorf("currentTime must be between 0 and %d", DailyTimeSlots-1)
		}
		if seen[page.CurrentTime] {
			return Response{}, fmt.Errorf("duplicate currentTime %d", page.CurrentTime)
		}
		seen[page.CurrentTime] = true
		if len(page.Payload) > maxDailyPageBytes {
			return Response{}, fmt.Errorf("currentTime %d payload exceeds %d bytes", page.CurrentTime, maxDailyPageBytes)
		}

		var envelope struct {
			FlightList json.RawMessage `json:"flightList"`
		}
		if err := json.Unmarshal(page.Payload, &envelope); err != nil {
			return Response{}, fmt.Errorf("decode currentTime %d payload: %w", page.CurrentTime, err)
		}
		flightList := bytes.TrimSpace(envelope.FlightList)
		if len(flightList) == 0 || bytes.Equal(flightList, []byte("null")) {
			return Response{}, fmt.Errorf("currentTime %d payload is missing flightList", page.CurrentTime)
		}

		var upstream UpstreamResponse
		if err := json.Unmarshal(page.Payload, &upstream); err != nil {
			return Response{}, fmt.Errorf("decode currentTime %d upstream response: %w", page.CurrentTime, err)
		}
		if upstream.Flag != "" && upstream.Flag != flag {
			return Response{}, fmt.Errorf("currentTime %d has flag %q, expected %q", page.CurrentTime, upstream.Flag, flag)
		}
		if upstream.Type != "" && upstream.Type != "cn" {
			return Response{}, fmt.Errorf("currentTime %d has type %q, expected %q", page.CurrentTime, upstream.Type, "cn")
		}
		if !upstreamValueMatches(upstream.CurrentDate, "1") {
			return Response{}, fmt.Errorf("currentTime %d has an unexpected currentDate", page.CurrentTime)
		}
		if !upstreamValueMatches(upstream.CurrentTime, strconv.Itoa(page.CurrentTime)) {
			return Response{}, fmt.Errorf("currentTime %d payload does not match its slot", page.CurrentTime)
		}

		query := Query{
			Type:        "cn",
			CurrentDate: "1",
			CurrentTime: strconv.Itoa(page.CurrentTime),
		}
		responses[page.CurrentTime] = normalizeResponse(collection.Direction, query, upstream)
	}

	return mergeDailyResponses(collection, responses), nil
}

func upstreamValueMatches(value any, expected string) bool {
	switch value := value.(type) {
	case nil:
		return true
	case string:
		return value == expected
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64) == expected
	default:
		return false
	}
}

func mergeDailyResponses(collection DailyCollection, responses []Response) Response {
	flightsByKey := make(map[string]Flight)
	orderedKeys := make([]string, 0)
	for _, response := range responses {
		for _, item := range response.Flights {
			key := dailyFlightKey(item)
			current, exists := flightsByKey[key]
			if !exists {
				orderedKeys = append(orderedKeys, key)
				flightsByKey[key] = item
				continue
			}
			flightsByKey[key] = preferredDailyFlight(current, item)
		}
	}

	mergedFlights := make([]Flight, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		mergedFlights = append(mergedFlights, flightsByKey[key])
	}

	return Response{
		Source:    "szairport",
		Direction: collection.Direction,
		Query: Query{
			Type:        "cn",
			CurrentDate: "1",
			CurrentTime: "0-12",
		},
		Total:       len(mergedFlights),
		Flights:     mergedFlights,
		RunID:       collection.RunID,
		ServiceDate: collection.ServiceDate,
		CollectedAt: collection.CollectedAt,
	}
}
