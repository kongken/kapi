package szx

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	bredis "butterfly.orx.me/core/store/redis"
	"github.com/kongken/kapi/internal/flight"
	redis "github.com/redis/go-redis/v9"
)

const baseURL = "https://www.szairport.com/szjchbjk/hbcx/flightInfo"

const (
	defaultFlightsCacheTTL = time.Minute
	defaultRedisKey        = "default"
	flightsCachePrefix     = "szx:flights:"
)

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client struct {
	httpClient HTTPDoer
	cache      responseCache
	cacheTTL   time.Duration
}

type responseCache interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
}

type redisCache struct {
	client *redis.Client
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
	return NewClientWithCache(httpClient, newRedisCache(), defaultFlightsCacheTTL)
}

func NewClientWithCache(httpClient HTTPDoer, cache responseCache, cacheTTL time.Duration) *Client {
	if cacheTTL <= 0 {
		cacheTTL = defaultFlightsCacheTTL
	}
	return &Client{httpClient: httpClient, cache: cache, cacheTTL: cacheTTL}
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
	currentTime, err := strconv.Atoi(query.CurrentTime)
	if err != nil {
		return errors.New("currentTime must be numeric")
	}
	if currentTime < 0 || currentTime > 12 {
		return errors.New("currentTime must be between 0 and 12")
	}
	return nil
}

func (c *Client) Fetch(ctx context.Context, direction string, rawQuery Query) (Response, error) {
	query := DefaultQuery(rawQuery)
	slog.Info("fetching szx flights", "direction", direction, "query", query)

	if cached, ok := c.loadCachedResponse(ctx, direction, query); ok {
		slog.Info("returning cached szx flights response", "direction", direction, "query", query, "total", cached.Total)
		return cached, nil
	}

	flag, err := directionFlag(direction)
	if err != nil {
		return Response{}, err
	}
	upstream, err := c.fetchUpstream(ctx, flag, query, true)
	if err != nil {
		return Response{}, err
	}

	response := normalizeResponse(direction, query, upstream)
	slog.Info("fetched szx flights from upstream", "direction", direction, "query", query, "total", response.Total)
	c.storeCachedResponse(ctx, direction, query, response)
	return response, nil
}

func newRedisCache() responseCache {
	client := bredis.GetClient(defaultRedisKey)
	if client == nil {
		slog.Warn("szx flights cache disabled: redis client not configured", "redis_key", defaultRedisKey)
		return nil
	}
	slog.Info("szx flights cache enabled", "redis_key", defaultRedisKey, "ttl", defaultFlightsCacheTTL)
	return &redisCache{client: client}
}

func (c *redisCache) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, key).Result()
}

func (c *redisCache) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

func (c *Client) loadCachedResponse(ctx context.Context, direction string, query Query) (Response, bool) {
	if c.cache == nil {
		slog.Info("skipping szx flights cache lookup: cache unavailable", "direction", direction, "query", query)
		return Response{}, false
	}

	cacheKey := flightsCacheKey(direction, query)
	value, err := c.cache.Get(ctx, cacheKey)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			slog.Info("szx flights cache miss", "direction", direction, "query", query, "key", cacheKey)
			return Response{}, false
		}
		slog.Warn("failed to load cached szx flights response", "key", cacheKey, "error", err)
		return Response{}, false
	}

	var response Response
	if err := json.Unmarshal([]byte(value), &response); err != nil {
		slog.Warn("failed to decode cached szx flights response", "key", cacheKey, "error", err)
		return Response{}, false
	}

	slog.Info("szx flights cache hit", "direction", direction, "query", query, "key", cacheKey)
	return response, true
}

func (c *Client) storeCachedResponse(ctx context.Context, direction string, query Query, response Response) {
	if c.cache == nil {
		slog.Info("skipping szx flights cache store: cache unavailable", "direction", direction, "query", query)
		return
	}

	payload, err := json.Marshal(response)
	if err != nil {
		slog.Warn("failed to encode cached szx flights response", "error", err)
		return
	}

	cacheKey := flightsCacheKey(direction, query)
	if err := c.cache.Set(ctx, cacheKey, string(payload), c.cacheTTL); err != nil {
		slog.Warn("failed to store cached szx flights response", "key", cacheKey, "error", err)
		return
	}

	slog.Info("stored szx flights response in cache", "direction", direction, "query", query, "key", cacheKey, "ttl", c.cacheTTL, "total", response.Total)
}

func flightsCacheKey(direction string, query Query) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%s", direction, query.Type, query.CurrentDate, query.CurrentTime, query.FlightNo)))
	return fmt.Sprintf("%s%x", flightsCachePrefix, sum)
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
