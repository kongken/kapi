package szx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kongken/kapi/internal/flight"
)

const baseURL = "https://www.szairport.com/szjchbjk/hbcx/flightInfo"

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client struct {
	httpClient HTTPDoer
}

type Query struct {
	Type        string `json:"type"`
	CurrentDate string `json:"currentDate"`
	CurrentTime string `json:"currentTime"`
	FlightNo    string `json:"flightNo"`
}

type FlightNumber struct {
	FlightNo string `json:"flightNo"`
}

type Logo struct {
	ImgSrc string `json:"imgSrc"`
}

type UpstreamFlight struct {
	StartSchemeTakeoffTime   string         `json:"startSchemeTakeoffTime"`
	TerminalSchemeLandinTime string         `json:"terminalSchemeLandinTime"`
	StartRealTakeoffTime     string         `json:"startRealTakeoffTime"`
	TerminalRealLandinTime   string         `json:"terminalRealLandinTime"`
	HBH                      []FlightNumber `json:"hbh"`
	Shareflightairport       []Logo         `json:"shareflightairport"`
	GateCode                 string         `json:"gateCode"`
	GateDesp                 string         `json:"gatedesp"`
	StartStation             string         `json:"startStationThreecharcode"`
	TerminalStation          string         `json:"terminalStationThreecharcode"`
	FltNormalStatus          string         `json:"fltNormalStatus"`
	FltNormalStatus2         string         `json:"fltNormalStatus2"`
	CKLS                     string         `json:"ckls"`
	FCESFCEE                 string         `json:"fces_fcee"`
	APOT                     string         `json:"apot"`
	BLLS                     string         `json:"blls"`
	CraftType                string         `json:"craftType"`
}

type UpstreamResponse struct {
	FlightList  []UpstreamFlight `json:"flightList"`
	Time        any              `json:"time"`
	Flag        string           `json:"flag"`
	Type        string           `json:"type"`
	CurrentDate any              `json:"currentDate"`
	CurrentTime any              `json:"currentTime"`
	HBXXHBH     string           `json:"hbxx_hbh"`
}

type Flight struct {
	FlightNumbers    []string       `json:"flightNumbers"`
	AirlineLogos     []string       `json:"airlineLogos"`
	PlannedDeparture string         `json:"plannedDepartureTime"`
	PlannedArrival   string         `json:"plannedArrivalTime"`
	ActualDeparture  string         `json:"actualDepartureTime"`
	ActualArrival    string         `json:"actualArrivalTime"`
	DepartureAirport string         `json:"departureAirport"`
	ArrivalAirport   string         `json:"arrivalAirport"`
	Terminal         string         `json:"terminal"`
	Gate             string         `json:"gate"`
	GateDescription  string         `json:"gateDescription"`
	BaggageBelt      string         `json:"baggageBelt"`
	CheckInArea      string         `json:"checkInArea"`
	CheckInWindow    string         `json:"checkInWindow"`
	StatusText       string         `json:"statusText"`
	StatusCode       string         `json:"statusCode"`
	AircraftType     string         `json:"aircraftType"`
	Raw              UpstreamFlight `json:"raw"`
}

type Response struct {
	Source    string           `json:"source"`
	Direction string           `json:"direction"`
	Query     Query            `json:"query"`
	Total     int              `json:"total"`
	Flights   []Flight         `json:"flights"`
	Raw       UpstreamResponse `json:"raw"`
}

func NewClient(httpClient HTTPDoer) *Client {
	return &Client{httpClient: httpClient}
}

func DefaultQuery(raw Query) Query {
	query := raw
	if query.Type == "" {
		query.Type = "cn"
	}
	if query.CurrentDate == "" {
		query.CurrentDate = "1"
	}
	if query.CurrentTime == "" {
		query.CurrentTime = "8"
	}
	return query
}

func ValidateQuery(query Query) error {
	if query.Type != "cn" && query.Type != "en" {
		return errors.New("type must be either 'cn' or 'en'")
	}
	if !isDigitsOnly(query.CurrentDate) {
		return errors.New("currentDate must be numeric")
	}
	if !isDigitsOnly(query.CurrentTime) {
		return errors.New("currentTime must be numeric")
	}
	return nil
}

func (c *Client) Fetch(ctx context.Context, direction string, rawQuery Query) (Response, error) {
	query := DefaultQuery(rawQuery)
	flag, err := directionFlag(direction)
	if err != nil {
		return Response{}, err
	}
	upstream, err := c.fetchUpstream(ctx, flag, query, true)
	if err != nil {
		return Response{}, err
	}
	return normalizeResponse(direction, query, upstream), nil
}

func (c *Client) fetchUpstream(ctx context.Context, flag string, query Query, canRetry bool) (UpstreamResponse, error) {
	requestURL := buildURL(flag, query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return UpstreamResponse{}, fmt.Errorf("build upstream request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "kapi-szx/1.0")

	slog.Info("requesting szairport flight upstream", "url", requestURL)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return UpstreamResponse{}, fmt.Errorf("request upstream: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return UpstreamResponse{}, fmt.Errorf("read upstream response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return UpstreamResponse{}, fmt.Errorf("upstream request failed with status %d", resp.StatusCode)
	}

	var payload UpstreamResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		if canRetry {
			retryCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			return c.fetchUpstream(retryCtx, flag, query, false)
		}
		return UpstreamResponse{}, errors.New("upstream returned invalid JSON")
	}

	return payload, nil
}

func buildURL(flag string, query Query) string {
	params := url.Values{}
	params.Set("type", query.Type)
	params.Set("flag", flag)
	params.Set("currentDate", query.CurrentDate)
	params.Set("currentTime", query.CurrentTime)
	params.Set("hbxx_hbh", query.FlightNo)

	return baseURL + "?" + params.Encode()
}

func normalizeResponse(direction string, query Query, upstream UpstreamResponse) Response {
	flights := make([]Flight, 0, len(upstream.FlightList))
	for _, item := range upstream.FlightList {
		flightNumbers := make([]string, 0, len(item.HBH))
		for _, number := range item.HBH {
			if number.FlightNo != "" {
				flightNumbers = append(flightNumbers, number.FlightNo)
			}
		}

		logos := make([]string, 0, len(item.Shareflightairport))
		for _, logo := range item.Shareflightairport {
			if logo.ImgSrc == "" {
				continue
			}
			logos = append(logos, resolveLogoURL(logo.ImgSrc))
		}

		flights = append(flights, Flight{
			FlightNumbers:    flightNumbers,
			AirlineLogos:     logos,
			PlannedDeparture: item.StartSchemeTakeoffTime,
			PlannedArrival:   item.TerminalSchemeLandinTime,
			ActualDeparture:  item.StartRealTakeoffTime,
			ActualArrival:    item.TerminalRealLandinTime,
			DepartureAirport: item.StartStation,
			ArrivalAirport:   item.TerminalStation,
			Terminal:         item.APOT,
			Gate:             item.GateCode,
			GateDescription:  item.GateDesp,
			BaggageBelt:      item.BLLS,
			CheckInArea:      item.CKLS,
			CheckInWindow:    item.FCESFCEE,
			StatusText:       item.FltNormalStatus,
			StatusCode:       item.FltNormalStatus2,
			AircraftType:     item.CraftType,
			Raw:              item,
		})
	}

	resultQuery := Query{
		Type:        query.Type,
		CurrentDate: query.CurrentDate,
		CurrentTime: query.CurrentTime,
		FlightNo:    query.FlightNo,
	}
	if upstream.Type != "" {
		resultQuery.Type = upstream.Type
	}
	if upstream.HBXXHBH != "" || query.FlightNo == "" {
		resultQuery.FlightNo = upstream.HBXXHBH
	}

	return Response{
		Source:    "szairport",
		Direction: direction,
		Query:     resultQuery,
		Total:     len(flights),
		Flights:   flights,
		Raw:       upstream,
	}
}

func directionFlag(direction string) (string, error) {
	switch direction {
	case "departure":
		return "D", nil
	case "arrival":
		return "A", nil
	default:
		return "", errors.New("direction must be departure or arrival")
	}
}

func resolveLogoURL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return "https://www.szairport.com" + path
}

// FetchFlights implements flight.Fetcher.
func (c *Client) FetchFlights(ctx context.Context, direction string) (*flight.FetchResult, error) {
	resp, err := c.Fetch(ctx, direction, Query{})
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}

	today := time.Now().UTC().Format("2006-01-02")
	var landed []flight.LandedFlight
	for _, f := range resp.Flights {
		if !isLanded(f) {
			continue
		}
		fdata, err := json.Marshal(f)
		if err != nil {
			continue
		}
		landed = append(landed, flight.LandedFlight{
			FlightNumbers: f.FlightNumbers,
			Date:          today,
			Data:          fdata,
		})
	}

	return &flight.FetchResult{
		Data:          data,
		LandedFlights: landed,
	}, nil
}

func isLanded(f Flight) bool {
	return f.ActualArrival != "" && f.ActualArrival != "--:--"
}

func NewDefaultClient() *Client {
	return NewClient(http.DefaultClient)
}

func isDigitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
