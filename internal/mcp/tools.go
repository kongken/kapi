package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kongken/kapi/internal/airports"
	"github.com/kongken/kapi/internal/flight"
)

// flightItem is the token-efficient flight representation returned by MCP tools.
// It mirrors the normalized v2 model but never includes the upstream Raw payloads.
type flightItem struct {
	FlightNumbers        []string `json:"flightNumbers"`
	AirlineLogos         []string `json:"airlineLogos,omitempty"`
	PlannedDepartureTime string   `json:"plannedDepartureTime,omitempty"`
	PlannedArrivalTime   string   `json:"plannedArrivalTime,omitempty"`
	ActualDepartureTime  string   `json:"actualDepartureTime,omitempty"`
	ActualArrivalTime    string   `json:"actualArrivalTime,omitempty"`
	DepartureAirport     string   `json:"departureAirport,omitempty"`
	ArrivalAirport       string   `json:"arrivalAirport,omitempty"`
	Terminal             string   `json:"terminal,omitempty"`
	Gate                 string   `json:"gate,omitempty"`
	GateDescription      string   `json:"gateDescription,omitempty"`
	BaggageBelt          string   `json:"baggageBelt,omitempty"`
	CheckInArea          string   `json:"checkInArea,omitempty"`
	CheckInWindow        string   `json:"checkInWindow,omitempty"`
	StatusText           string   `json:"statusText,omitempty"`
	StatusCode           string   `json:"statusCode,omitempty"`
	AircraftType         string   `json:"aircraftType,omitempty"`
}

func newFlightItem(f airports.Flight) flightItem {
	return flightItem{
		FlightNumbers:        f.FlightNumbers,
		AirlineLogos:         f.AirlineLogos,
		PlannedDepartureTime: f.PlannedDepartureTime,
		PlannedArrivalTime:   f.PlannedArrivalTime,
		ActualDepartureTime:  f.ActualDepartureTime,
		ActualArrivalTime:    f.ActualArrivalTime,
		DepartureAirport:     f.DepartureAirport,
		ArrivalAirport:       f.ArrivalAirport,
		Terminal:             f.Terminal,
		Gate:                 f.Gate,
		GateDescription:      f.GateDescription,
		BaggageBelt:          f.BaggageBelt,
		CheckInArea:          f.CheckInArea,
		CheckInWindow:        f.CheckInWindow,
		StatusText:           f.StatusText,
		StatusCode:           f.StatusCode,
		AircraftType:         f.AircraftType,
	}
}

// ---------- list_airports ----------

type listAirportsOut struct {
	Total    int                    `json:"total"`
	Airports []airports.AirportInfo `json:"airports"`
}

func (s *service) listAirports(ctx context.Context, req *gomcp.CallToolRequest, _ struct{}) (*gomcp.CallToolResult, listAirportsOut, error) {
	list := s.registry.List()
	return nil, listAirportsOut{Total: len(list), Airports: list}, nil
}

// ---------- search_flights ----------

type searchFlightsIn struct {
	Airport   string  `json:"airport" jsonschema:"airport IATA code in lowercase, e.g. 'szx' or 'can'"`
	Direction string  `json:"direction" jsonschema:"flight direction, either 'departure' or 'arrival'"`
	Date      *string `json:"date,omitempty" jsonschema:"optional date filter, numeric format e.g. 20260501 (not supported by every airport)"`
	Time      *string `json:"time,omitempty" jsonschema:"optional time-of-day filter, numeric HHMM"`
	FlightNo  *string `json:"flightNo,omitempty" jsonschema:"optional flight number filter, e.g. CZ3456"`
	Lang      *string `json:"lang,omitempty" jsonschema:"optional response language, 'cn' (default) or 'en'"`
}

type searchFlightsOut struct {
	Source    string       `json:"source"`
	Airport   string       `json:"airport"`
	Direction string       `json:"direction"`
	Total     int          `json:"total"`
	Items     []flightItem `json:"items"`
}

func (s *service) searchFlights(ctx context.Context, req *gomcp.CallToolRequest, in searchFlightsIn) (*gomcp.CallToolResult, searchFlightsOut, error) {
	query := airports.FlightQuery{
		Direction: in.Direction,
		Lang:      "cn",
		Date:      deref(in.Date),
		Time:      deref(in.Time),
		FlightNo:  deref(in.FlightNo),
	}
	if lang := deref(in.Lang); lang != "" {
		query.Lang = lang
	}
	if err := airports.ValidateFlightQuery(query); err != nil {
		return nil, searchFlightsOut{}, fmt.Errorf("invalid arguments: %w", err)
	}

	provider, ok := s.registry.Get(in.Airport)
	if !ok {
		return nil, searchFlightsOut{}, fmt.Errorf("airport %q is not supported; use list_airports to see available airports", in.Airport)
	}

	response, err := provider.GetFlights(ctx, query)
	if err != nil {
		return nil, searchFlightsOut{}, fmt.Errorf("upstream fetch failed for %s %s: %w", in.Airport, in.Direction, err)
	}

	items := make([]flightItem, 0, len(response.Items))
	for _, f := range response.Items {
		items = append(items, newFlightItem(f))
	}
	return nil, searchFlightsOut{
		Source:    response.Source,
		Airport:   response.Airport,
		Direction: response.Direction,
		Total:     len(items),
		Items:     items,
	}, nil
}

// ---------- get_flight_status ----------

type getFlightStatusIn struct {
	FlightNo string  `json:"flightNo" jsonschema:"flight number including carrier code, e.g. CZ3456"`
	Airport  *string `json:"airport,omitempty" jsonschema:"limit the lookup to one airport IATA code; all supported airports are searched when omitted"`
	Date     *string `json:"date,omitempty" jsonschema:"optional date filter, numeric format e.g. 20260501"`
}

type getFlightStatusOut struct {
	Found    bool          `json:"found"`
	Query    string        `json:"query"`
	Searched []string      `json:"searched"`
	Matches  []statusMatch `json:"matches"`
}

type statusMatch struct {
	Airport   string     `json:"airport"`
	Direction string     `json:"direction"`
	Flight    flightItem `json:"flight"`
}

// maxStatusMatches bounds the result size for pathological queries.
const maxStatusMatches = 20

func (s *service) getFlightStatus(ctx context.Context, req *gomcp.CallToolRequest, in getFlightStatusIn) (*gomcp.CallToolResult, getFlightStatusOut, error) {
	target := strings.ToUpper(strings.TrimSpace(in.FlightNo))
	if target == "" {
		return nil, getFlightStatusOut{}, fmt.Errorf("flightNo must not be empty")
	}

	codes := s.registry.Codes()
	if requested := strings.ToLower(strings.TrimSpace(deref(in.Airport))); requested != "" {
		if _, ok := s.registry.Get(requested); !ok {
			return nil, getFlightStatusOut{}, fmt.Errorf("airport %q is not supported; use list_airports to see available airports", requested)
		}
		codes = []string{requested}
	}

	out := getFlightStatusOut{Query: target, Searched: codes}
	for _, code := range codes {
		for _, direction := range []string{"departure", "arrival"} {
			if len(out.Matches) >= maxStatusMatches {
				break
			}
			provider, ok := s.registry.Get(code)
			if !ok {
				continue
			}
			response, err := provider.GetFlights(ctx, airports.FlightQuery{
				Direction: direction,
				Lang:      "cn",
				Date:      deref(in.Date),
				FlightNo:  target,
			})
			if err != nil {
				continue // try next airport/direction
			}
			for _, f := range response.Items {
				if matchesFlightNo(f, target) {
					out.Matches = append(out.Matches, statusMatch{
						Airport:   code,
						Direction: direction,
						Flight:    newFlightItem(f),
					})
					if len(out.Matches) >= maxStatusMatches {
						break
					}
				}
			}
		}
	}
	out.Found = len(out.Matches) > 0
	return nil, out, nil
}

func matchesFlightNo(f airports.Flight, target string) bool {
	for _, no := range f.FlightNumbers {
		if strings.EqualFold(strings.TrimSpace(no), target) {
			return true
		}
	}
	return false
}

// ---------- get_today_flights ----------

type getTodayFlightsIn struct {
	Airport   string  `json:"airport" jsonschema:"airport IATA code in lowercase, e.g. 'szx' or 'can'"`
	Direction string  `json:"direction" jsonschema:"flight direction, either 'departure' or 'arrival'"`
	Status    *string `json:"status,omitempty" jsonschema:"optional status filter matched against each flight's status text/code (substring match), e.g. '取消', '延误', 'delayed'"`
	Limit     *int    `json:"limit,omitempty" jsonschema:"max items returned after filtering (default 20, max 200); counts always cover the full day"`
}

// dailySnapshot parses both szx and can snapshots — their JSON field names are identical.
type dailySnapshot struct {
	Source    string           `json:"source"`
	Direction string           `json:"direction"`
	Total     int              `json:"total"`
	Flights   []snapshotFlight `json:"flights"`
}

type snapshotFlight = flightItem

const (
	defaultTodayLimit = 20
	maxTodayLimit     = 200
)

type getTodayFlightsOut struct {
	Airport      string           `json:"airport"`
	Direction    string           `json:"direction"`
	Source       string           `json:"source"`
	Total        int              `json:"total"`
	StatusCounts map[string]int   `json:"statusCounts"`
	Returned     int              `json:"returned"`
	Items        []snapshotFlight `json:"items"`
}

func (s *service) getTodayFlights(ctx context.Context, req *gomcp.CallToolRequest, in getTodayFlightsIn) (*gomcp.CallToolResult, getTodayFlightsOut, error) {
	code := strings.ToLower(strings.TrimSpace(in.Airport))
	if _, ok := s.registry.Get(code); !ok {
		return nil, getTodayFlightsOut{}, fmt.Errorf("airport %q is not supported; use list_airports to see available airports", in.Airport)
	}
	if in.Direction != "departure" && in.Direction != "arrival" {
		return nil, getTodayFlightsOut{}, fmt.Errorf("direction must be either 'departure' or 'arrival'")
	}

	limit := defaultTodayLimit
	if in.Limit != nil {
		limit = *in.Limit
	}
	if limit < 0 {
		limit = defaultTodayLimit
	}
	if limit > maxTodayLimit {
		limit = maxTodayLimit
	}

	data, err := s.loader.Load(ctx, code, in.Direction)
	if err != nil {
		return nil, getTodayFlightsOut{}, fmt.Errorf("today's snapshot for %s %s is unavailable: %w", code, in.Direction, err)
	}

	var snapshot dailySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, getTodayFlightsOut{}, fmt.Errorf("decode today's snapshot for %s %s: %w", code, in.Direction, err)
	}

	statusFilter := ""
	if in.Status != nil {
		statusFilter = strings.TrimSpace(*in.Status)
	}

	out := getTodayFlightsOut{
		Airport:      code,
		Direction:    in.Direction,
		Source:       snapshot.Source,
		Total:        len(snapshot.Flights),
		StatusCounts: make(map[string]int),
		Items:        make([]snapshotFlight, 0, min(limit, len(snapshot.Flights))),
	}
	for _, f := range snapshot.Flights {
		key := f.StatusText
		if key == "" {
			key = f.StatusCode
		}
		if key == "" {
			key = "unknown"
		}
		out.StatusCounts[key]++

		if statusFilter != "" && !matchesStatus(f, statusFilter) {
			continue
		}
		if len(out.Items) < limit {
			out.Items = append(out.Items, f)
		}
	}
	out.Returned = len(out.Items)
	return nil, out, nil
}

func matchesStatus(f snapshotFlight, filter string) bool {
	return strings.Contains(f.StatusText, filter) || strings.Contains(strings.ToLower(f.StatusCode), strings.ToLower(filter))
}

// ---------- get_delay_trend ----------

type getDelayTrendIn struct {
	Airport string `json:"airport" jsonschema:"airport IATA code; currently only 'szx' is supported"`
}

func (s *service) getDelayTrend(ctx context.Context, req *gomcp.CallToolRequest, in getDelayTrendIn) (*gomcp.CallToolResult, flight.DelayTrendResponse, error) {
	code := strings.ToLower(strings.TrimSpace(in.Airport))
	if code != "szx" {
		return nil, flight.DelayTrendResponse{}, fmt.Errorf("delay trend is currently only available for 'szx'")
	}

	snapshots := make(map[string][]byte, 2)
	for _, direction := range []string{"departure", "arrival"} {
		data, err := s.loader.Load(ctx, code, direction)
		if err != nil {
			return nil, flight.DelayTrendResponse{}, fmt.Errorf("today's %s snapshot is unavailable: %w", direction, err)
		}
		snapshots[direction] = data
	}

	trend, err := flight.BuildSZXDelayTrend(snapshots)
	if err != nil {
		return nil, flight.DelayTrendResponse{}, fmt.Errorf("build delay trend: %w", err)
	}
	return nil, trend, nil
}

// ---------- get_weather ----------

type getWeatherIn struct {
	Airport string `json:"airport" jsonschema:"airport IATA code in lowercase, e.g. 'szx' or 'can'"`
}

type weatherItem struct {
	Date    string `json:"date,omitempty"`
	High    string `json:"high,omitempty"`
	Low     string `json:"low,omitempty"`
	Type    string `json:"type,omitempty"`
	IconURL string `json:"iconUrl,omitempty"`
}

type getWeatherOut struct {
	Source  string        `json:"source"`
	Airport string        `json:"airport"`
	Total   int           `json:"total"`
	Items   []weatherItem `json:"items"`
}

func (s *service) getWeather(ctx context.Context, req *gomcp.CallToolRequest, in getWeatherIn) (*gomcp.CallToolResult, getWeatherOut, error) {
	provider, ok := s.registry.Get(in.Airport)
	if !ok {
		return nil, getWeatherOut{}, fmt.Errorf("airport %q is not supported; use list_airports to see available airports", in.Airport)
	}

	response, err := provider.GetWeather(ctx)
	if err != nil {
		return nil, getWeatherOut{}, fmt.Errorf("weather fetch failed for %s: %w", in.Airport, err)
	}

	items := make([]weatherItem, 0, len(response.Items))
	for _, w := range response.Items {
		items = append(items, weatherItem{Date: w.Date, High: w.High, Low: w.Low, Type: w.Type, IconURL: w.IconURL})
	}
	return nil, getWeatherOut{Source: response.Source, Airport: response.Airport, Total: len(items), Items: items}, nil
}

// ---------- helpers ----------

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
