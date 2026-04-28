package szx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
)

type testHTTPDoer func(req *http.Request) (*http.Response, error)

func (f testHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

type memoryCache struct {
	values   map[string]string
	getCalls int
	setCalls int
	lastTTL  time.Duration
	lastKey  string
	getErr   error
	setErr   error
}

func (c *memoryCache) Get(_ context.Context, key string) (string, error) {
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

func (c *memoryCache) Set(_ context.Context, key string, value string, ttl time.Duration) error {
	c.setCalls++
	c.lastKey = key
	c.lastTTL = ttl
	if c.setErr != nil {
		return c.setErr
	}
	if c.values == nil {
		c.values = make(map[string]string)
	}
	c.values[key] = value
	return nil
}

func TestFetchUsesCachedFlightsResponse(t *testing.T) {
	query := DefaultQuery(Query{CurrentTime: "12", FlightNo: "CZ5387"})
	cacheKey := flightsCacheKey("departure", query)
	cache := &memoryCache{
		values: map[string]string{
			cacheKey: `{"source":"szairport","direction":"departure","query":{"type":"cn","currentDate":"1","currentTime":"12","flightNo":"CZ5387"},"total":1,"flights":[{"flightNumbers":["CZ5387"]}],"raw":{"flightList":[]}}`,
		},
	}

	client := NewClientWithCache(testHTTPDoer(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected upstream call to %s", req.URL.String())
		return nil, nil
	}), cache, time.Minute)

	resp, err := client.Fetch(context.Background(), "departure", Query{CurrentTime: "12", FlightNo: "CZ5387"})
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected cached response total 1, got %d", resp.Total)
	}
	if len(resp.Flights) != 1 || len(resp.Flights[0].FlightNumbers) != 1 || resp.Flights[0].FlightNumbers[0] != "CZ5387" {
		t.Fatalf("expected cached flight number CZ5387, got %+v", resp.Flights)
	}
	if cache.setCalls != 0 {
		t.Fatalf("expected cache hit without cache write, got %d set calls", cache.setCalls)
	}
}

func TestFetchStoresFlightsResponseInCache(t *testing.T) {
	cache := &memoryCache{}
	upstreamCalls := 0
	client := NewClientWithCache(testHTTPDoer(func(req *http.Request) (*http.Response, error) {
		upstreamCalls++
		body := `{"flightList":[{"startSchemeTakeoffTime":"16:00","terminalSchemeLandinTime":"18:40","startRealTakeoffTime":"16:12","terminalRealLandinTime":"--:--","hbh":[{"flightNo":"CZ5387"}],"shareflightairport":[{"imgSrc":"/app-editor/ewebeditor/uploadfile/airlineslogo/CZ.png"}],"gateCode":"324","gatedesp":"","startStationThreecharcode":"深圳","terminalStationThreecharcode":"成都双流","fltNormalStatus":"已于16:12起飞","fltNormalStatus2":"#","ckls":"A,B01-B12","fces_fcee":"14:00-15:22","apot":"T3","blls":"","craftType":"A21N"}],"type":"cn","currentDate":1,"currentTime":12,"hbxx_hbh":"CZ5387"}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}), cache, time.Minute)

	resp, err := client.Fetch(context.Background(), "departure", Query{CurrentTime: "12", FlightNo: "CZ5387"})
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if upstreamCalls != 1 {
		t.Fatalf("expected one upstream call, got %d", upstreamCalls)
	}
	if resp.Total != 1 {
		t.Fatalf("expected normalized response total 1, got %d", resp.Total)
	}
	if cache.setCalls != 1 {
		t.Fatalf("expected one cache write, got %d", cache.setCalls)
	}
	if cache.lastTTL != time.Minute {
		t.Fatalf("expected cache ttl %s, got %s", time.Minute, cache.lastTTL)
	}
	if cache.lastKey == "" {
		t.Fatal("expected cache key to be recorded")
	}
	if cached := cache.values[cache.lastKey]; !strings.Contains(cached, `"direction":"departure"`) {
		t.Fatalf("expected cached normalized response, got %s", cached)
	}
}

func TestFetchIgnoresCacheReadErrors(t *testing.T) {
	cache := &memoryCache{getErr: errors.New("boom")}
	upstreamCalls := 0
	client := NewClientWithCache(testHTTPDoer(func(req *http.Request) (*http.Response, error) {
		upstreamCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"flightList":[],"type":"cn","currentDate":1,"currentTime":12}`)),
			Header:     make(http.Header),
		}, nil
	}), cache, time.Minute)

	if _, err := client.Fetch(context.Background(), "arrival", Query{CurrentTime: "12"}); err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if upstreamCalls != 1 {
		t.Fatalf("expected upstream fallback after cache error, got %d calls", upstreamCalls)
	}
}

func TestValidateQueryRejectsCurrentTimeOutsideVerifiedRange(t *testing.T) {
	tests := []struct {
		name  string
		query Query
	}{
		{
			name:  "negative not allowed",
			query: Query{Type: "cn", CurrentDate: "1", CurrentTime: "-1"},
		},
		{
			name:  "above verified upstream range",
			query: Query{Type: "cn", CurrentDate: "1", CurrentTime: "13"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateQuery(tt.query); err == nil {
				t.Fatalf("expected validation error for query %+v", tt.query)
			}
		})
	}
}

func TestFetchDailyFlightsMergesTimeSlots(t *testing.T) {
	client := NewClientWithCache(testHTTPDoer(func(req *http.Request) (*http.Response, error) {
		currentTime := req.URL.Query().Get("currentTime")
		body := `{"flightList":[],"type":"cn","currentDate":1,"currentTime":` + currentTime + `}`

		switch currentTime {
		case "0":
			body = `{"flightList":[{"startSchemeTakeoffTime":"16:00","terminalSchemeLandinTime":"18:40","startRealTakeoffTime":"16:12","terminalRealLandinTime":"--:--","hbh":[{"flightNo":"CZ5387"}],"shareflightairport":[],"gateCode":"324","gatedesp":"","startStationThreecharcode":"深圳","terminalStationThreecharcode":"成都双流","fltNormalStatus":"已于16:12起飞","fltNormalStatus2":"#","ckls":"A,B01-B12","fces_fcee":"14:00-15:22","apot":"T3","blls":"","craftType":"A21N"}],"type":"cn","currentDate":1,"currentTime":0}`
		case "12":
			body = `{"flightList":[{"startSchemeTakeoffTime":"16:00","terminalSchemeLandinTime":"18:40","startRealTakeoffTime":"--:--","terminalRealLandinTime":"--:--","hbh":[{"flightNo":"CZ5387"}],"shareflightairport":[],"gateCode":"","gatedesp":"","startStationThreecharcode":"深圳","terminalStationThreecharcode":"成都双流","fltNormalStatus":"","fltNormalStatus2":"#","ckls":"","fces_fcee":"","apot":"T3","blls":"","craftType":"A21N"},{"startSchemeTakeoffTime":"17:00","terminalSchemeLandinTime":"19:40","startRealTakeoffTime":"--:--","terminalRealLandinTime":"--:--","hbh":[{"flightNo":"CA1303"}],"shareflightairport":[],"gateCode":"524","gatedesp":"Near lounge","startStationThreecharcode":"深圳","terminalStationThreecharcode":"北京首都","fltNormalStatus":"","fltNormalStatus2":"","ckls":"C","fces_fcee":"15:00-16:20","apot":"T3","blls":"","craftType":"B738"}],"type":"cn","currentDate":1,"currentTime":12}`
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}), nil, time.Minute)

	data, err := client.FetchDailyFlights(context.Background(), "departure")
	if err != nil {
		t.Fatalf("FetchDailyFlights returned error: %v", err)
	}

	var response Response
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("failed to decode daily response: %v", err)
	}

	if response.Query.CurrentTime != "0-12" {
		t.Fatalf("expected merged currentTime range, got %q", response.Query.CurrentTime)
	}
	if response.Total != 2 {
		t.Fatalf("expected merged total 2, got %d", response.Total)
	}
	if len(response.Flights) != 2 {
		t.Fatalf("expected 2 merged flights, got %d", len(response.Flights))
	}
	if response.Flights[0].ActualDeparture != "16:12" {
		t.Fatalf("expected richer duplicate to win, got actual departure %q", response.Flights[0].ActualDeparture)
	}
	if got := response.Flights[1].FlightNumbers[0]; got != "CA1303" {
		t.Fatalf("expected second merged flight CA1303, got %q", got)
	}
	if got := response.Flights[1].Gate; got != "524" {
		t.Fatalf("expected CA1303 gate to be preserved, got %q", got)
	}
}
