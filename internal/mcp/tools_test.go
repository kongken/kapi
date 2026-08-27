package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kongken/kapi/internal/airports"
)

type stubProvider struct {
	code       string
	info       airports.AirportInfo
	flights    airports.FlightsResponse
	flightsErr error
	weather    airports.WeatherResponse
	lastQuery  airports.FlightQuery
}

func (p *stubProvider) Code() string               { return p.code }
func (p *stubProvider) Info() airports.AirportInfo { return p.info }
func (p *stubProvider) GetFlights(ctx context.Context, query airports.FlightQuery) (airports.FlightsResponse, error) {
	p.lastQuery = query
	if p.flightsErr != nil {
		return airports.FlightsResponse{}, p.flightsErr
	}
	return p.flights, nil
}
func (p *stubProvider) GetWeather(ctx context.Context) (airports.WeatherResponse, error) {
	return p.weather, nil
}

type stubLoader struct {
	data map[string][]byte
	err  error
}

func (l *stubLoader) Load(ctx context.Context, airportCode string, direction string) ([]byte, error) {
	if l.err != nil {
		return nil, l.err
	}
	data, ok := l.data[airportCode+":"+direction]
	if !ok {
		return nil, errNotFound
	}
	return data, nil
}

var errNotFound = &notFoundError{}

type notFoundError struct{}

func (*notFoundError) Error() string { return "daily snapshot not found" }

const testDepartureSnapshot = `{
  "source": "test-source",
  "direction": "departure",
  "total": 3,
  "flights": [
    {"flightNumbers":["CZ3456"],"plannedDepartureTime":"08:30","statusText":"起飞","statusCode":"DEP"},
    {"flightNumbers":["ZH9101"],"plannedDepartureTime":"09:15","statusText":"延误","statusCode":"DEL"},
    {"flightNumbers":["MU1234"],"plannedDepartureTime":"10:00","statusText":"取消","statusCode":"CAN"}
  ]
}`

const testArrivalSnapshot = `{
  "source": "test-source",
  "direction": "arrival",
  "total": 3,
  "flights": [
    {"flightNumbers":["CZ3456"],"plannedArrivalTime":"11:30","statusText":"到达","statusCode":"ARR"},
    {"flightNumbers":["ZH9101"],"plannedArrivalTime":"12:15","statusText":"延误","statusCode":"DEL"},
    {"flightNumbers":["MU1234"],"plannedArrivalTime":"13:00","statusText":"取消","statusCode":"CAN"}
  ]
}`

func newTestService() *service {
	registry := airports.NewRegistry(
		&stubProvider{
			code: "szx",
			info: airports.AirportInfo{Code: "szx", NameCn: "深圳宝安国际机场", City: "深圳", HasWeather: true},
			flights: airports.FlightsResponse{
				Source:    "test-source",
				Airport:   "szx",
				Direction: "departure",
				Items: []airports.Flight{
					{
						FlightNumbers:        []string{"CZ3456"},
						PlannedDepartureTime: "08:30",
						StatusText:           "已于08:42起飞",
						Raw:                  map[string]any{"secret": "upstream-payload"},
					},
				},
			},
			weather: airports.WeatherResponse{
				Source: "test-weather",
				Items:  []airports.Weather{{Date: "05月01日", High: "30℃", Low: "24℃", Type: "多云", Raw: map[string]any{"x": 1}}},
			},
		},
	)
	return &service{
		registry: registry,
		loader: &stubLoader{data: map[string][]byte{
			"szx:departure": []byte(testDepartureSnapshot),
			"szx:arrival":   []byte(testArrivalSnapshot),
		}},
	}
}

func TestListAirports(t *testing.T) {
	_, out, err := newTestService().listAirports(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatalf("listAirports: %v", err)
	}
	if out.Total != 1 || len(out.Airports) != 1 || out.Airports[0].Code != "szx" {
		t.Fatalf("unexpected airports output: %+v", out)
	}
}

func TestSearchFlightsTrimsRawPayload(t *testing.T) {
	_, out, err := newTestService().searchFlights(context.Background(), nil, searchFlightsIn{Airport: "szx", Direction: "departure"})
	if err != nil {
		t.Fatalf("searchFlights: %v", err)
	}
	if out.Total != 1 || len(out.Items) != 1 {
		t.Fatalf("unexpected search output: %+v", out)
	}
	item := out.Items[0]
	if item.FlightNumbers[0] != "CZ3456" || item.PlannedDepartureTime != "08:30" {
		t.Fatalf("unexpected mapped flight: %+v", item)
	}
	encoded, _ := json.Marshal(item)
	if strings.Contains(string(encoded), "upstream-payload") {
		t.Fatalf("raw upstream payload leaked into tool output")
	}
}

func TestSearchFlightsValidation(t *testing.T) {
	svc := newTestService()

	_, _, err := svc.searchFlights(context.Background(), nil, searchFlightsIn{Airport: "szx", Direction: "sideways"})
	if err == nil || !strings.Contains(err.Error(), "invalid_query") {
		t.Fatalf("expected invalid_query-coded error, got %v", err)
	}
	_, _, err = svc.searchFlights(context.Background(), nil, searchFlightsIn{Airport: "pek", Direction: "departure"})
	if err == nil || !strings.Contains(err.Error(), "airport_not_supported") {
		t.Fatalf("expected airport_not_supported-coded error, got %v", err)
	}
	_, _, err = svc.searchFlights(context.Background(), nil, searchFlightsIn{Airport: "szx", Direction: "departure", Zone: strPtr("cargo")})
	if err == nil || !strings.Contains(err.Error(), "invalid_query") {
		t.Fatalf("expected invalid_query-coded error for bad zone, got %v", err)
	}
}

func TestSearchFlightsPassesZoneToProvider(t *testing.T) {
	provider := &stubProvider{
		code: "pvg",
		info: airports.AirportInfo{Code: "pvg", Zones: []string{"domestic", "international"}},
		flights: airports.FlightsResponse{
			Source:    "test-source",
			Airport:   "pvg",
			Direction: "departure",
			Items:     []airports.Flight{{FlightNumbers: []string{"MU9007"}, Zone: "international"}},
		},
	}
	svc := &service{
		registry: airports.NewRegistry(provider),
		loader:   &stubLoader{},
	}

	_, out, err := svc.searchFlights(context.Background(), nil, searchFlightsIn{
		Airport:   "pvg",
		Direction: "departure",
		Zone:      strPtr("international"),
	})
	if err != nil {
		t.Fatalf("searchFlights: %v", err)
	}
	if provider.lastQuery.Zone != "international" {
		t.Fatalf("expected zone passed to provider, got %q", provider.lastQuery.Zone)
	}
	if len(out.Items) != 1 || out.Items[0].Zone != "international" {
		t.Fatalf("expected zone on returned item, got %+v", out.Items)
	}
}

func TestSearchFlightsRejectsUnsupportedZoneForAirport(t *testing.T) {
	svc := newTestService() // szx advertises no zones
	_, _, err := svc.searchFlights(context.Background(), nil, searchFlightsIn{
		Airport:   "szx",
		Direction: "departure",
		Zone:      strPtr("domestic"),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid_query") || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected invalid_query not-supported error, got %v", err)
	}
}

func TestGetFlightStatusFindsAcrossDirections(t *testing.T) {
	_, out, err := newTestService().getFlightStatus(context.Background(), nil, getFlightStatusIn{FlightNo: "cz3456"})
	if err != nil {
		t.Fatalf("getFlightStatus: %v", err)
	}
	if !out.Found || len(out.Matches) != 2 { // departure + arrival both served by stub
		t.Fatalf("expected matches in both directions, got %+v", out)
	}
	if out.Matches[0].Flight.FlightNumbers[0] != "CZ3456" {
		t.Fatalf("unexpected match: %+v", out.Matches[0])
	}
	if len(out.Issues) != 0 {
		t.Fatalf("expected no probe issues from healthy providers, got %+v", out.Issues)
	}

	_, miss, err := newTestService().getFlightStatus(context.Background(), nil, getFlightStatusIn{FlightNo: "XX0000"})
	if err != nil || miss.Found {
		t.Fatalf("expected not found, got err=%v found=%v", err, miss.Found)
	}
}

func TestGetTodayFlightsSummaryAndFilter(t *testing.T) {
	svc := newTestService()

	_, full, err := svc.getTodayFlights(context.Background(), nil, getTodayFlightsIn{Airport: "szx", Direction: "departure"})
	if err != nil {
		t.Fatalf("getTodayFlights: %v", err)
	}
	if full.Total != 3 || full.Returned != 3 {
		t.Fatalf("unexpected totals: %+v", full)
	}
	if full.StatusCounts["起飞"] != 1 || full.StatusCounts["延误"] != 1 || full.StatusCounts["取消"] != 1 {
		t.Fatalf("unexpected status counts: %v", full.StatusCounts)
	}

	_, cancelled, err := svc.getTodayFlights(context.Background(), nil, getTodayFlightsIn{Airport: "szx", Direction: "departure", Status: strPtr("取消")})
	if err != nil {
		t.Fatalf("filtered getTodayFlights: %v", err)
	}
	if cancelled.Returned != 1 || cancelled.Items[0].FlightNumbers[0] != "MU1234" {
		t.Fatalf("unexpected filtered result: %+v", cancelled)
	}

	_, limited, err := svc.getTodayFlights(context.Background(), nil, getTodayFlightsIn{Airport: "szx", Direction: "departure", Limit: intPtr(1)})
	if err != nil {
		t.Fatalf("limited getTodayFlights: %v", err)
	}
	if limited.Returned != 1 || limited.Total != 3 {
		t.Fatalf("limit should only cap items, got %+v", limited)
	}
}

func TestGetTodayFlightsZoneFilter(t *testing.T) {
	svc := &service{
		registry: airports.NewRegistry(&stubProvider{
			code: "pvg",
			info: airports.AirportInfo{Code: "pvg", Zones: []string{"domestic", "international"}},
		}),
	}
	// seed a snapshot carrying per-flight zones
	svc.loader = &stubLoader{data: map[string][]byte{
		"pvg:departure": []byte(`{
		  "source": "shairport",
		  "direction": "departure",
		  "total": 3,
		  "flights": [
		    {"flightNumbers":["MU5101"],"plannedDepartureTime":"07:00","zone":"domestic"},
		    {"flightNumbers":["MU9007"],"plannedDepartureTime":"09:00","zone":"international"},
		    {"flightNumbers":["CA930"],"plannedDepartureTime":"11:00","zone":"international"}
		  ]
		}`),
	}}

	_, out, err := svc.getTodayFlights(context.Background(), nil, getTodayFlightsIn{
		Airport:   "pvg",
		Direction: "departure",
		Zone:      strPtr("international"),
	})
	if err != nil {
		t.Fatalf("getTodayFlights: %v", err)
	}
	if out.Total != 3 {
		t.Fatalf("expected full-day total 3, got %d", out.Total)
	}
	if out.Returned != 2 || len(out.Items) != 2 {
		t.Fatalf("expected 2 international items, got %d", out.Returned)
	}
	for _, item := range out.Items {
		if item.Zone != "international" {
			t.Fatalf("expected only international items, got %+v", item)
		}
	}

	_, _, err = svc.getTodayFlights(context.Background(), nil, getTodayFlightsIn{
		Airport:   "pvg",
		Direction: "departure",
		Zone:      strPtr("cargo"),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid_query") {
		t.Fatalf("expected invalid_query-coded error for bad zone, got %v", err)
	}
}

func TestGetDelayTrend(t *testing.T) {
	svc := newTestService()

	if _, _, err := svc.getDelayTrend(context.Background(), nil, getDelayTrendIn{Airport: "can"}); err == nil {
		t.Fatal("expected unsupported airport error for delay trend")
	}

	_, trend, err := svc.getDelayTrend(context.Background(), nil, getDelayTrendIn{Airport: "szx"})
	if err != nil {
		t.Fatalf("getDelayTrend: %v", err)
	}
	if trend.Total.Flights != 6 { // 3 flights x 2 directions
		t.Fatalf("unexpected trend totals: %+v", trend.Total)
	}
}

func TestGetWeatherTrimsRaw(t *testing.T) {
	_, out, err := newTestService().getWeather(context.Background(), nil, getWeatherIn{Airport: "szx"})
	if err != nil {
		t.Fatalf("getWeather: %v", err)
	}
	if out.Total != 1 || out.Items[0].Type != "多云" {
		t.Fatalf("unexpected weather output: %+v", out)
	}
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), `"x"`) {
		t.Fatalf("raw upstream payload leaked into weather output")
	}
}

func TestGetFlightStatusRecordsUpstreamFailures(t *testing.T) {
	svc := &service{
		registry: airports.NewRegistry(&stubProvider{
			code:       "szx",
			flightsErr: errors.New("upstream boom"),
		}),
		loader: &stubLoader{},
	}

	_, out, err := svc.getFlightStatus(context.Background(), nil, getFlightStatusIn{FlightNo: "CZ3456"})
	if err != nil {
		t.Fatalf("getFlightStatus should not fail on provider errors: %v", err)
	}
	if out.Found || len(out.Matches) != 0 {
		t.Fatalf("expected no matches, got %+v", out)
	}
	// Both directions failed and must be reported instead of silently skipped.
	if len(out.Issues) != 2 {
		t.Fatalf("expected 2 recorded issues (departure+arrival), got %+v", out.Issues)
	}
	for _, issue := range out.Issues {
		if issue.Airport != "szx" || !strings.Contains(issue.Error, "upstream boom") {
			t.Fatalf("unexpected issue entry: %+v", issue)
		}
	}
}

func strPtr(v string) *string { return &v }
func intPtr(v int) *int       { return &v }
