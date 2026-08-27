// Package pvg is the Shanghai Pudong (PVG) upstream client.
//
// It speaks to the Shanghai airport official site (shairport.com), which serves
// BOTH Shanghai airports (PVG and SHA) through one endpoint:
//
//	POST https://www.shairport.com/AvinexApi/OldFlightHandler.aspx
//
// The endpoint requires a browser User-Agent and the flights page as Referer,
// otherwise it returns 403. Requests are plain form-urlencoded, no auth.
//
// Domestic vs international is expressed through the `direction` form field:
//
//	1 = domestic   departure, 2 = domestic   arrival
//	3 = intl/HKMO  departure, 4 = intl/HKMO  arrival
//
// The response is an envelope JSON whose `data.flightList` is itself a JSON
// string of flight objects (field names are Chinese, e.g. 主航班号, 计划出发时间).
package pvg

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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	bredis "butterfly.orx.me/core/store/redis"
	redis "github.com/redis/go-redis/v9"
)

const (
	baseURL    = "https://www.shairport.com"
	flightsURL = baseURL + "/AvinexApi/OldFlightHandler.aspx"

	// airportCode is sent as airCities2. The same endpoint also serves SHA
	// (虹桥); airCities2=SHA would select it instead.
	airportCode = "PVG"

	// Headers required by the upstream site.
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	referer   = baseURL + "/flights/index.html"

	// pageSize=500 is accepted by the upstream and returns a full day in one
	// page for PVG; pagination still loops for safety.
	pageSize = 500

	// maxPages bounds the pagination loop defensively.
	maxPages = 50

	defaultFlightsCacheTTL = time.Minute
	defaultRedisKey        = "default"
	flightsCachePrefix     = "pvg:flights:"

	// Zone value literals matching the v2 airports contract
	// (airports.ZoneDomestic / airports.ZoneInternational).
	zoneDomestic      = "domestic"
	zoneInternational = "international"

	// Upstream direction codes.
	dirDomesticDeparture = 1
	dirDomesticArrival   = 2
	dirIntlDeparture     = 3
	dirIntlArrival       = 4
)

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// UpstreamFlight mirrors one item of data.flightList. Field names are the
// original Chinese keys from the shairport site.
type UpstreamFlight struct {
	RowNumber          string `json:"peta_rn"`
	ID                 string `json:"Id"`
	Direction          string `json:"航向"`
	MainFlightNo       string `json:"主航班号"`
	SubFlightNo        string `json:"子航班号"`
	PlannedArrival     string `json:"计划到达时间"`
	ActualArrival      string `json:"实际到达时间"`
	EstimatedArrival   string `json:"预计到达时间"`
	PlannedDeparture   string `json:"计划出发时间"`
	ActualDeparture    string `json:"实际出发时间"`
	EstimatedDeparture string `json:"预计出发时间"`
	Departure          string `json:"出发地"`
	Stopover           string `json:"经停地"`
	Destination        string `json:"目的地"`
	Terminal           string `json:"候机楼"`
	DivertedAirport    string `json:"改降机场"`
	GateStatus         string `json:"登机门状态"`
	Status             string `json:"状态"`
	CheckInCounter     string `json:"值机柜台"`
	BaggageBelt        string `json:"行李传送带"`
	CheckInArea1       string `json:"值机区域1"`
	CheckInArea2       string `json:"值机区域2"`
	CheckInArea3       string `json:"值机区域3"`
	Airline            string `json:"航空公司"`
	DisplayTime        string `json:"显示计划时间"`
	DisplayArrivalTime string `json:"显示计划到达时间"`
	TimeDisplay        string `json:"时间显示"`
	DepartureCode      string `json:"出发地代号"`
	DestinationCode    string `json:"目的地代号"`
}

// UpstreamResponse is the envelope returned by OldFlightHandler.aspx.
type UpstreamResponse struct {
	Success bool `json:"success"`
	Status  int  `json:"status"`
	Data    struct {
		PageIndex  int    `json:"pageIndex"`
		PageSize   int    `json:"pageSize"`
		TotalPages int    `json:"totalPages"`
		TotalItems int    `json:"totalItems"`
		FlightList string `json:"flightList"` // JSON-encoded string, may be ""
	} `json:"data"`
}

// Query is the v1/normalized flight query echoed back to callers.
type Query struct {
	Direction string `json:"direction"`
	Zone      string `json:"zone"`
	Date      string `json:"date"`
	FlightNo  string `json:"flightNo"`
}

// Flight is the normalized PVG flight payload (mirrors the v2 model).
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
	Zone             string         `json:"zone"`
	Raw              UpstreamFlight `json:"raw"`
}

// Response is the normalized PVG flight response.
type Response struct {
	Source    string   `json:"source"`
	Direction string   `json:"direction"`
	Query     Query    `json:"query"`
	Total     int      `json:"total"`
	Flights   []Flight `json:"flights"`
	Raw       any      `json:"raw,omitempty"`
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

func NewClient(httpClient HTTPDoer) *Client {
	return NewClientWithCache(httpClient, newRedisCache(), defaultFlightsCacheTTL)
}

func NewClientWithCache(httpClient HTTPDoer, cache responseCache, cacheTTL time.Duration) *Client {
	if cacheTTL <= 0 {
		cacheTTL = defaultFlightsCacheTTL
	}
	return &Client{httpClient: httpClient, cache: cache, cacheTTL: cacheTTL}
}

func NewDefaultClient() *Client {
	return NewClient(http.DefaultClient)
}

// taggedFlight carries an upstream flight together with the zone (domestic /
// international) of the upstream direction it was fetched from.
type taggedFlight struct {
	upstream UpstreamFlight
	zone     string
}

// Fetch returns normalized flights for a direction/zone/date/flightNo query.
//
// List queries (no flightNo) map direction+zone to the upstream direction
// codes: an empty zone merges domestic and international (both directions), a
// concrete zone queries only its direction.
//
// Flight-number lookups replicate the official site: the upstream ignores the
// direction filter for flight-number queries and returns the day's matching
// record(s) in every direction, so we send a single cross-direction request
// (direction="") and filter by the 航向 (departure/arrival) field to avoid
// duplicates. Zone is tagged from the requested zone (or left unknown when
// none was requested, since a cross-direction query cannot classify).
func (c *Client) Fetch(ctx context.Context, direction string, zone string, date string, flightNo string) (Response, error) {
	query := Query{Direction: direction, Zone: zone, Date: date, FlightNo: flightNo}

	if cached, ok := c.loadCachedResponse(ctx, query); ok {
		slog.Info("returning cached pvg flights response", "direction", direction, "zone", zone, "total", cached.Total)
		return cached, nil
	}

	var all []taggedFlight
	if strings.TrimSpace(flightNo) != "" {
		var err error
		all, err = c.fetchFlightNumber(ctx, direction, zone, date, flightNo)
		if err != nil {
			return Response{}, err
		}
	} else {
		dirs, err := upstreamDirections(direction, zone)
		if err != nil {
			return Response{}, err
		}
		for _, dir := range dirs {
			tagged, err := c.fetchAllPages(ctx, dir, date, flightNo)
			if err != nil {
				return Response{}, err
			}
			all = append(all, tagged...)
		}
	}

	response := normalizeResponse(direction, query, all)
	slog.Info("fetched pvg flights from upstream", "direction", direction, "zone", zone, "total", response.Total)
	c.storeCachedResponse(ctx, query, response)
	return response, nil
}

// fetchFlightNumber runs the single cross-direction flight-number query and
// keeps only records matching the requested direction (via 航向).
func (c *Client) fetchFlightNumber(ctx context.Context, direction string, zone string, date string, flightNo string) ([]taggedFlight, error) {
	items, err := c.fetchAllPages(ctx, 0, date, flightNo)
	if err != nil {
		return nil, err
	}
	filtered := make([]taggedFlight, 0, len(items))
	for _, t := range items {
		if !matchesDirectionField(direction, t.upstream.Direction) {
			continue
		}
		filtered = append(filtered, t)
	}
	// A cross-direction query cannot classify flights by zone; tag unknown
	// unless the caller pinned a zone.
	for i := range filtered {
		filtered[i].zone = zone
	}
	return filtered, nil
}

// fetchAllPages pulls every page for one upstream direction and tags each
// flight with the zone that direction represents.
func (c *Client) fetchAllPages(ctx context.Context, direction int, date string, flightNo string) ([]taggedFlight, error) {
	zone := zoneFromDirection(direction)
	result := make([]taggedFlight, 0)

	for page := 1; page <= maxPages; page++ {
		upstream, err := c.fetchPage(ctx, direction, page, date, flightNo)
		if err != nil {
			return nil, err
		}
		items, err := parseFlightList(upstream.Data.FlightList)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			result = append(result, taggedFlight{upstream: item, zone: zone})
		}
		if len(items) == 0 || page >= upstream.Data.TotalPages {
			break
		}
	}
	return result, nil
}

func (c *Client) fetchPage(ctx context.Context, direction int, page int, date string, flightNo string) (UpstreamResponse, error) {
	form := url.Values{}
	form.Set("action", "GetData")
	form.Set("currentPage", strconv.Itoa(page))
	form.Set("pageSize", strconv.Itoa(pageSize))
	form.Set("flightType", "1") // 客班 (passenger)
	// direction 0 means "no direction filter" (all 航向), used by
	// flight-number lookups exactly like the official site does.
	if direction == 0 {
		form.Set("direction", "")
	} else {
		form.Set("direction", strconv.Itoa(direction))
	}
	form.Set("airCities", "")
	form.Set("airCities2", airportCode)
	form.Set("airCompanies", "")
	form.Set("timeDays", dateOrZero(date))
	form.Set("timeSpan", "00:00-23:59")
	form.Set("flightNum", flightNo)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, flightsURL, strings.NewReader(form.Encode()))
	if err != nil {
		return UpstreamResponse{}, fmt.Errorf("build upstream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", referer)
	req.Header.Set("User-Agent", userAgent)

	slog.Info("requesting shairport flight upstream", "url", flightsURL, "direction", direction, "page", page)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return UpstreamResponse{}, fmt.Errorf("request upstream: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return UpstreamResponse{}, fmt.Errorf("read upstream response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return UpstreamResponse{}, fmt.Errorf("upstream request failed with status %d", resp.StatusCode)
	}

	var payload UpstreamResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return UpstreamResponse{}, fmt.Errorf("decode upstream response: %w", err)
	}
	if !payload.Success {
		return UpstreamResponse{}, fmt.Errorf("upstream returned success=false (status %d)", payload.Status)
	}
	return payload, nil
}

// upstreamDirections maps a v2 direction+zone to the shairport direction
// codes to query. zone "" means both domestic and international.
func upstreamDirections(direction string, zone string) ([]int, error) {
	switch direction {
	case "departure":
		switch zone {
		case "":
			return []int{dirDomesticDeparture, dirIntlDeparture}, nil
		case zoneDomestic:
			return []int{dirDomesticDeparture}, nil
		case zoneInternational:
			return []int{dirIntlDeparture}, nil
		}
	case "arrival":
		switch zone {
		case "":
			return []int{dirDomesticArrival, dirIntlArrival}, nil
		case zoneDomestic:
			return []int{dirDomesticArrival}, nil
		case zoneInternational:
			return []int{dirIntlArrival}, nil
		}
	}
	return nil, fmt.Errorf("unsupported direction %q or zone %q", direction, zone)
}

// zoneFromDirection derives the v2 zone from the upstream direction code.
func zoneFromDirection(direction int) string {
	switch direction {
	case dirDomesticDeparture, dirDomesticArrival:
		return zoneDomestic
	case dirIntlDeparture, dirIntlArrival:
		return zoneInternational
	}
	return ""
}

// matchesDirectionField reports whether an upstream 航向 value ("出发"/"到达")
// satisfies the requested v2 direction. Unknown values are kept.
func matchesDirectionField(direction string, dirField string) bool {
	if dirField == "" {
		return true
	}
	if direction == "arrival" {
		return dirField == "到达"
	}
	return dirField == "出发"
}

// dateOrZero maps an empty v2 date to today (timeDays=0).
func dateOrZero(date string) string {
	if date == "" {
		return "0"
	}
	return date
}

// parseFlightList decodes data.flightList, which is a JSON-encoded string and
// may be empty (meaning no flights).
func parseFlightList(encoded string) ([]UpstreamFlight, error) {
	if strings.TrimSpace(encoded) == "" {
		return nil, nil
	}
	var items []UpstreamFlight
	if err := json.Unmarshal([]byte(encoded), &items); err != nil {
		return nil, fmt.Errorf("decode flightList: %w", err)
	}
	return items, nil
}

// normalizeResponse builds the normalized response from tagged upstream items,
// sorting by the relevant planned time (departure or arrival).
func normalizeResponse(direction string, query Query, tagged []taggedFlight) Response {
	flights := make([]Flight, 0, len(tagged))
	for _, t := range tagged {
		item := t.upstream

		flightNumbers := []string{strings.TrimSpace(item.MainFlightNo)}
		for _, no := range extractSubFlightNumbers(item.SubFlightNo) {
			flightNumbers = append(flightNumbers, no)
		}

		flights = append(flights, Flight{
			FlightNumbers:    flightNumbers,
			AirlineLogos:     []string{},
			PlannedDeparture: formatTime(item.PlannedDeparture),
			PlannedArrival:   formatTime(item.PlannedArrival),
			ActualDeparture:  formatTime(item.ActualDeparture),
			ActualArrival:    formatTime(item.ActualArrival),
			DepartureAirport: item.Departure,
			ArrivalAirport:   item.Destination,
			Terminal:         normalizeTerminal(item.Terminal),
			Gate:             item.GateStatus,
			GateDescription:  "",
			BaggageBelt:      item.BaggageBelt,
			CheckInArea:      item.CheckInCounter,
			CheckInWindow:    joinCheckInWindow(item.CheckInArea1, item.CheckInArea2),
			StatusText:       item.Status,
			StatusCode:       "",
			AircraftType:     "",
			Zone:             t.zone,
			Raw:              item,
		})
	}

	sort.SliceStable(flights, func(i, j int) bool {
		if direction == "arrival" {
			return flights[i].PlannedArrival < flights[j].PlannedArrival
		}
		return flights[i].PlannedDeparture < flights[j].PlannedDeparture
	})

	return Response{
		Source:    "shairport",
		Direction: direction,
		Query:     query,
		Total:     len(flights),
		Flights:   flights,
	}
}

// FetchDailyFlights returns today's full merged day (domestic + international)
// as JSON bytes, suitable for the daily snapshot syncer.
func (c *Client) FetchDailyFlights(ctx context.Context, direction string) ([]byte, error) {
	response, err := c.Fetch(ctx, direction, "", "", "")
	if err != nil {
		return nil, fmt.Errorf("fetch pvg daily flights: %w", err)
	}
	return json.Marshal(response)
}

// ---------- normalization helpers ----------

// formatTime reduces "2026-08-27 06:15:00" to "06:15".
func formatTime(datetime string) string {
	if datetime == "" {
		return ""
	}
	parts := strings.SplitN(datetime, " ", 2)
	if len(parts) == 2 {
		clock := parts[1]
		if len(clock) >= 5 {
			return clock[:5]
		}
	}
	return datetime
}

// normalizeTerminal reduces "浦东(T1)" / "浦东(T1-S1)" to "T1" / "T1-S1".
func normalizeTerminal(s string) string {
	open := strings.Index(s, "(")
	close := strings.Index(s, ")")
	if open >= 0 && close > open {
		return s[open+1 : close]
	}
	return s
}

// joinCheckInWindow merges the two 值机区域 fields: "1A01"+"1A32" -> "1A01-1A32".
func joinCheckInWindow(a1 string, a2 string) string {
	switch {
	case a1 == "" && a2 == "":
		return ""
	case a2 == "":
		return a1
	case a1 == "":
		return a2
	default:
		return a1 + "-" + a2
	}
}

var liTagRe = regexp.MustCompile(`(?s)<li[^>]*>(.*?)</li>`)
var htmlTagRe = regexp.MustCompile(`(?s)<[^>]+>`)
var nbspRe = regexp.MustCompile(`&[a-zA-Z#0-9]+;`)

// extractSubFlightNumbers parses the 子航班号 field, which the upstream renders
// as HTML (e.g. <marquee><li>EY5902</li><li>HO5674</li></marquee>).
func extractSubFlightNumbers(html string) []string {
	if strings.TrimSpace(html) == "" {
		return nil
	}
	out := []string{}
	for _, m := range liTagRe.FindAllStringSubmatch(html, -1) {
		v := cleanFlightNo(m[1])
		if v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		if v := cleanFlightNo(html); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func cleanFlightNo(s string) string {
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = nbspRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// ---------- cache ----------

func newRedisCache() responseCache {
	client := bredis.GetClient(defaultRedisKey)
	if client == nil {
		slog.Warn("pvg flights cache disabled: redis client not configured", "redis_key", defaultRedisKey)
		return nil
	}
	slog.Info("pvg flights cache enabled", "redis_key", defaultRedisKey, "ttl", defaultFlightsCacheTTL)
	return &redisCache{client: client}
}

func (c *redisCache) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, key).Result()
}

func (c *redisCache) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

func flightsCacheKey(query Query) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s", query.Direction, query.Zone, query.Date, query.FlightNo)))
	return fmt.Sprintf("%s%x", flightsCachePrefix, sum)
}

func (c *Client) loadCachedResponse(ctx context.Context, query Query) (Response, bool) {
	if c.cache == nil {
		return Response{}, false
	}
	cacheKey := flightsCacheKey(query)
	value, err := c.cache.Get(ctx, cacheKey)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Warn("failed to load cached pvg flights response", "key", cacheKey, "error", err)
		}
		return Response{}, false
	}
	var response Response
	if err := json.Unmarshal([]byte(value), &response); err != nil {
		slog.Warn("failed to decode cached pvg flights response", "key", cacheKey, "error", err)
		return Response{}, false
	}
	return response, true
}

func (c *Client) storeCachedResponse(ctx context.Context, query Query, response Response) {
	if c.cache == nil {
		return
	}
	payload, err := json.Marshal(response)
	if err != nil {
		slog.Warn("failed to encode pvg flights response for cache", "error", err)
		return
	}
	cacheKey := flightsCacheKey(query)
	if err := c.cache.Set(ctx, cacheKey, string(payload), c.cacheTTL); err != nil {
		slog.Warn("failed to store pvg flights response in cache", "key", cacheKey, "error", err)
		return
	}
	slog.Info("stored pvg flights response in cache", "key", cacheKey, "ttl", c.cacheTTL, "total", response.Total)
}
