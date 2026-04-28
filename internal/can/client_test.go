package can

import (
	"context"
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
}

func (c *memoryCache) Get(_ context.Context, key string) (string, error) {
	c.getCalls++
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
	if c.values == nil {
		c.values = make(map[string]string)
	}
	c.values[key] = value
	return nil
}

func TestSignMessage(t *testing.T) {
	pk, err := parsePrivateKey(rsaPrivateKeyPEM)
	if err != nil {
		t.Fatalf("failed to parse private key: %v", err)
	}

	sig, err := signMessage(pk, "test message")
	if err != nil {
		t.Fatalf("failed to sign message: %v", err)
	}
	if sig == "" {
		t.Fatal("expected non-empty signature")
	}
}

func TestSortJSONKeys(t *testing.T) {
	input := `{"pageSize":15,"type":"1","day":0,"depOrArr":"1","terminal":"","pageNum":1}`
	result := sortJSONKeys(input)

	expected := `{"day":0,"depOrArr":"1","pageNum":1,"pageSize":15,"terminal":"","type":"1"}`
	if result != expected {
		t.Fatalf("expected %s, got %s", expected, result)
	}
}

func TestDirectionToDepOrArr(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		hasErr   bool
	}{
		{"departure", "1", false},
		{"arrival", "2", false},
		{"invalid", "", true},
	}
	for _, tt := range tests {
		result, err := directionToDepOrArr(tt.input)
		if tt.hasErr && err == nil {
			t.Fatalf("expected error for input %q", tt.input)
		}
		if !tt.hasErr && err != nil {
			t.Fatalf("unexpected error for input %q: %v", tt.input, err)
		}
		if result != tt.expected {
			t.Fatalf("expected %q for input %q, got %q", tt.expected, tt.input, result)
		}
	}
}

func TestFormatTime(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"2026-04-28 08:30:00", "08:30"},
		{"2026-04-28 14:00:00", "14:00"},
		{"", ""},
		{"08:30", "08:30"},
	}
	for _, tt := range tests {
		result := formatTime(tt.input)
		if result != tt.expected {
			t.Fatalf("formatTime(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestFetchNormalizesResponse(t *testing.T) {
	cache := &memoryCache{}
	upstreamCalls := 0
	client := NewClientWithCache(testHTTPDoer(func(req *http.Request) (*http.Response, error) {
		upstreamCalls++

		if req.Header.Get("Signature") == "" {
			t.Fatal("expected Signature header")
		}
		if req.Header.Get("Timestamp") == "" {
			t.Fatal("expected Timestamp header")
		}
		if req.Header.Get("Nonce") == "" {
			t.Fatal("expected Nonce header")
		}

		body := `{"code":"200","msg":"success","data":{"list":[{"flightNo":"CZ3456","flightDate":"2026-04-28","flightId":"12345","airline":"CZ","airlineCn":"南方航空","airlineEn":"China Southern","setoffTimePlan":"2026-04-28 08:30:00","setoffTimeAct":"2026-04-28 08:35:00","setoffTimePred":"2026-04-28 08:30:00","arriTimePlan":"2026-04-28 11:00:00","arriTimeAct":"","boardingTime":"","orgCityCn":"广州","orgCityEn":"Guangzhou","orgCity":"CAN","dstCityCn":"北京","dstCityEn":"Beijing","dstCity":"PEK","terminal":"T2","depTerminal":"T2","checkInCounter":"A01-A10","boardingGate":"B12","baggageTable":"","arrExit":"","flightStatusCn":"已起飞","flightStatusEn":"Departed","planeModle":"B738","depOrArr":"D","domesticOrIntl":"D","flightTask":"W/Z","isStop":0,"isShare":1,"transferCityNameCn":"","transferCityNameEn":"","shareFlight":["MU1234"],"carouselFLights":[{"flightNo":"CZ3456","logo":"/logos/CZ.png"}]}]}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}), cache, time.Minute)

	resp, err := client.Fetch(context.Background(), "departure", "cn")
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if resp.Source != "baiyunairport" {
		t.Fatalf("expected source baiyunairport, got %q", resp.Source)
	}
	if resp.Total != 1 {
		t.Fatalf("expected total 1, got %d", resp.Total)
	}

	flight := resp.Flights[0]
	if flight.FlightNumbers[0] != "CZ3456" {
		t.Fatalf("expected flight number CZ3456, got %q", flight.FlightNumbers[0])
	}
	if len(flight.FlightNumbers) != 2 || flight.FlightNumbers[1] != "MU1234" {
		t.Fatalf("expected share flight MU1234, got %v", flight.FlightNumbers)
	}
	if flight.PlannedDeparture != "08:30" {
		t.Fatalf("expected planned departure 08:30, got %q", flight.PlannedDeparture)
	}
	if flight.ActualDeparture != "08:35" {
		t.Fatalf("expected actual departure 08:35, got %q", flight.ActualDeparture)
	}
	if flight.DepartureAirport != "广州" {
		t.Fatalf("expected departure airport 广州, got %q", flight.DepartureAirport)
	}
	if flight.ArrivalAirport != "北京" {
		t.Fatalf("expected arrival airport 北京, got %q", flight.ArrivalAirport)
	}
	if flight.Terminal != "T2" {
		t.Fatalf("expected terminal T2, got %q", flight.Terminal)
	}
	if flight.Gate != "B12" {
		t.Fatalf("expected gate B12, got %q", flight.Gate)
	}
	if flight.CheckInArea != "A01-A10" {
		t.Fatalf("expected check-in area A01-A10, got %q", flight.CheckInArea)
	}
	if flight.StatusText != "已起飞" {
		t.Fatalf("expected status 已起飞, got %q", flight.StatusText)
	}
	if flight.AircraftType != "B738" {
		t.Fatalf("expected aircraft type B738, got %q", flight.AircraftType)
	}
	if len(flight.AirlineLogos) != 1 || !strings.Contains(flight.AirlineLogos[0], "/logos/CZ.png") {
		t.Fatalf("expected resolved logo URL, got %v", flight.AirlineLogos)
	}

	if cache.setCalls != 1 {
		t.Fatalf("expected one cache write, got %d", cache.setCalls)
	}
}

func TestFetchUsesCachedResponse(t *testing.T) {
	cacheKey := flightsCacheKey("departure", "cn")
	cache := &memoryCache{
		values: map[string]string{
			cacheKey: `{"source":"baiyunairport","direction":"departure","query":{},"total":1,"flights":[{"flightNumbers":["CZ3456"]}]}`,
		},
	}

	client := NewClientWithCache(testHTTPDoer(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected upstream call")
		return nil, nil
	}), cache, time.Minute)

	resp, err := client.Fetch(context.Background(), "departure", "cn")
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected cached total 1, got %d", resp.Total)
	}
	if cache.setCalls != 0 {
		t.Fatalf("expected no cache writes, got %d", cache.setCalls)
	}
}

func TestResolveLogoURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/logos/CZ.png", baseURL + "/logos/CZ.png"},
		{"https://cdn.example.com/logo.png", "https://cdn.example.com/logo.png"},
		{"http://cdn.example.com/logo.png", "http://cdn.example.com/logo.png"},
	}
	for _, tt := range tests {
		result := resolveLogoURL(tt.input)
		if result != tt.expected {
			t.Fatalf("resolveLogoURL(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
