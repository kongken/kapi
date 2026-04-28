package can

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	bredis "butterfly.orx.me/core/store/redis"
	redis "github.com/redis/go-redis/v9"

	"github.com/google/uuid"
)

const (
	baseURL    = "https://www.baiyunairport.com"
	flightsURL = baseURL + "/byairport-flight/flight/list"

	defaultFlightsCacheTTL = time.Minute
	defaultRedisKey        = "default"
	flightsCachePrefix     = "can:flights:"
)

// Embedded from the Baiyun Airport frontend JS bundle.
const rsaPrivateKeyPEM = `-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQCdgJNfUFPDNJsL
HObB1JMu7E1+nuwkFHmXnBU2AOM2dweE+tpmViZo90w+YQIuIS8MoVz60AGHbLE8
BYcdxQEKmPsqq0Lq/1ltIdp1YcO9W60qSxwpZS+7o73ljRrrtOXcE1UUpH5l07Fh
ziCIRDI/4ODCA8AJ1kV6IyfPNM2Fes3BEqhMOgw4Z5i4pZHnb4Nm+4kEXmyM+UgQ
cShcXZA/dx5MXKA2Bbb0I0G6HS3D4nMhnm6IgYWEyT8ngenMOyy+ysBuHWt2j9Cp
AGLWRyqHigFcKTlP5BSIkU+8sqssab1jvDg2F8MXWuupwF43OVARgHofiwQBAHPo
PfTfPlMvAgMBAAECggEBAJKQpZNasrfCak0LFgllgZl2uB6OUPy6OPRGgM6CQO3c
EhlDPp1gqdmf10ltCJRYuOmt91JG4kVddgh+tF+VhgSQm5n3SQxZlqQhjqMQ2Q+L
Ejd7Mberu6GHHB1TE6wn6IbFTrUo5Z5oQnbbVBa6L3CWGVEyIDCHPpwLvu3pGx+L
083dNQUiF8WcSGybl1h4ZapAGdndPYJReKYccNBYu5IzTEjtG3VpMHl56hD8fPV8
SStYv4sEffyCbze5/KvG3WlG+8n1WzBRMAN1U8Qk3JlMM/g5Y2tL1elI2pQRmjH8
EVxNUzB9Ob/qk2N6pF4KwhDWjILkHdoXilHMgP5x0gECgYEAy9O9ShtRNwXdFzHe
v+buyjvWWvwTVRUBehe8BWO1QaZ4c/INw1Ks4pgoKvXyU1DRx5OloIx6BWDbs00O
1W1cDue2I/Ymvx5Q/XJmZK4eR2U3a2dmKLKVhCXhJ3y02R/OZ2xQHV3NZXqz88kf
rEmEKYTW9q2gVsZa82XpQhKnBYECgYEAxdFJ55AkU7VfzpV1x68NUetomB3OWxyq
Cugn3STLNx7Jw6FaK3dwRz0eKIbwCRxtlluZmxWX0jWSvj3cyLRBIKTD8atUfJW2
+ESKZb/i961HhhQjXqNfGQpmMdEazNqv0sDzQ5jHHIjc63oty/FjckcC+AaDGZIJ
VGCet5J5kK8CgYBm2R/Bfgk792R5KLvaHz/MoebmoB1tKB1HqyQ/n/E9AC/1aWUS
cuwzpk1WaCXvbm98Af9oBJopjpctYSuj+/ugtcDNYo5oj3aUfJ44HTfAFM2jD1iY
HoydUrPKxf1HNepje17tgoB6vTCCSbEGsU3T2WjSrgei4ZHREVJi+aB3gQKBgEy8
rm2sxdrPHjZWVlU6+/DOYEm6LkW77d7DRkuMLWTZha1lF0SLVbvc4qkYB1+RbpWI
PSMjEj0SWTWBa/dTrXwLTpOeQez+avcOJ53m/RXVW0yQ3VOmDor5NMGYe0wCfXhF
L1kGmB7inMigIcnefxRipa0vYYX217WqsYdGw++zAoGBALKswyV5j1GjVjN+fS1t
N9R0x+S7cKBqW6Bwj6aAdo4+spmRn9WK4h9Zk2k7BMUiqJKTce6RdW0Ep+aTErRs
LL0sBHArhQdaQvq0yS57BJUZm3ASrOpp3wkQdDejS3YEKiIQSG2kNFRanh8RbtbA
ac7pfLikyQm795/qF0H9YHgF
-----END PRIVATE KEY-----`

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client struct {
	httpClient HTTPDoer
	privateKey *rsa.PrivateKey
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

type FlightRequest struct {
	Type      string `json:"type"`
	Terminal  string `json:"terminal"`
	Day       int    `json:"day"`
	DepOrArr  string `json:"depOrArr"`
	PageNum   int    `json:"pageNum"`
	PageSize  int    `json:"pageSize"`
}

type UpstreamCarouselFlight struct {
	FlightNo string `json:"flightNo"`
	Logo     string `json:"logo"`
}

type UpstreamFlight struct {
	FlightNo        string                   `json:"flightNo"`
	FlightDate      string                   `json:"flightDate"`
	FlightID        string                   `json:"flightId"`
	Airline         string                   `json:"airline"`
	AirlineCn       string                   `json:"airlineCn"`
	AirlineEn       string                   `json:"airlineEn"`
	SetoffTimePlan  string                   `json:"setoffTimePlan"`
	SetoffTimeAct   string                   `json:"setoffTimeAct"`
	SetoffTimePred  string                   `json:"setoffTimePred"`
	ArriTimePlan    string                   `json:"arriTimePlan"`
	ArriTimeAct     string                   `json:"arriTimeAct"`
	BoardingTime    string                   `json:"boardingTime"`
	OrgCityCn       string                   `json:"orgCityCn"`
	OrgCityEn       string                   `json:"orgCityEn"`
	OrgCity         string                   `json:"orgCity"`
	DstCityCn       string                   `json:"dstCityCn"`
	DstCityEn       string                   `json:"dstCityEn"`
	DstCity         string                   `json:"dstCity"`
	Terminal        string                   `json:"terminal"`
	DepTerminal     string                   `json:"depTerminal"`
	CheckInCounter  string                   `json:"checkInCounter"`
	BoardingGate    string                   `json:"boardingGate"`
	BaggageTable    string                   `json:"baggageTable"`
	ArrExit         string                   `json:"arrExit"`
	FlightStatusCn  string                   `json:"flightStatusCn"`
	FlightStatusEn  string                   `json:"flightStatusEn"`
	PlaneModle      string                   `json:"planeModle"`
	DepOrArr        string                   `json:"depOrArr"`
	DomesticOrIntl  string                   `json:"domesticOrIntl"`
	FlightTask      string                   `json:"flightTask"`
	IsStop          int                      `json:"isStop"`
	IsShare         int                      `json:"isShare"`
	TransferCityCn  string                   `json:"transferCityNameCn"`
	TransferCityEn  string                   `json:"transferCityNameEn"`
	ShareFlight     []string                 `json:"shareFlight"`
	CarouselFLights []UpstreamCarouselFlight `json:"carouselFLights"`
}

type UpstreamListResponse struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		List []UpstreamFlight `json:"list"`
	} `json:"data"`
}

type Flight struct {
	FlightNumbers    []string        `json:"flightNumbers"`
	AirlineLogos     []string        `json:"airlineLogos"`
	PlannedDeparture string          `json:"plannedDepartureTime"`
	PlannedArrival   string          `json:"plannedArrivalTime"`
	ActualDeparture  string          `json:"actualDepartureTime"`
	ActualArrival    string          `json:"actualArrivalTime"`
	DepartureAirport string          `json:"departureAirport"`
	ArrivalAirport   string          `json:"arrivalAirport"`
	Terminal         string          `json:"terminal"`
	Gate             string          `json:"gate"`
	GateDescription  string          `json:"gateDescription"`
	BaggageBelt      string          `json:"baggageBelt"`
	CheckInArea      string          `json:"checkInArea"`
	CheckInWindow    string          `json:"checkInWindow"`
	StatusText       string          `json:"statusText"`
	StatusCode       string          `json:"statusCode"`
	AircraftType     string          `json:"aircraftType"`
	Raw              UpstreamFlight  `json:"raw"`
}

type Response struct {
	Source    string          `json:"source"`
	Direction string         `json:"direction"`
	Query     FlightRequest  `json:"query"`
	Total     int            `json:"total"`
	Flights   []Flight       `json:"flights"`
	Raw       any            `json:"raw"`
}

func NewClient(httpClient HTTPDoer) *Client {
	return NewClientWithCache(httpClient, newRedisCache(), defaultFlightsCacheTTL)
}

func NewClientWithCache(httpClient HTTPDoer, cache responseCache, cacheTTL time.Duration) *Client {
	if cacheTTL <= 0 {
		cacheTTL = defaultFlightsCacheTTL
	}
	pk, err := parsePrivateKey(rsaPrivateKeyPEM)
	if err != nil {
		slog.Error("failed to parse CAN private key", "error", err)
	}
	return &Client{httpClient: httpClient, privateKey: pk, cache: cache, cacheTTL: cacheTTL}
}

func NewDefaultClient() *Client {
	return NewClient(http.DefaultClient)
}

func (c *Client) Fetch(ctx context.Context, direction string, lang string) (Response, error) {
	depOrArr, err := directionToDepOrArr(direction)
	if err != nil {
		return Response{}, err
	}

	query := FlightRequest{
		Type:     "1",
		Terminal: "",
		Day:      0,
		DepOrArr: depOrArr,
		PageNum:  1,
		PageSize: 500,
	}

	cacheKey := flightsCacheKey(direction, lang)
	if cached, ok := c.loadCachedResponse(ctx, cacheKey); ok {
		slog.Info("returning cached CAN flights response", "direction", direction, "total", cached.Total)
		return cached, nil
	}

	var allFlights []UpstreamFlight
	for page := 1; page <= 20; page++ {
		query.PageNum = page
		upstream, err := c.fetchUpstream(ctx, query)
		if err != nil {
			return Response{}, err
		}
		if len(upstream.Data.List) == 0 {
			break
		}
		allFlights = append(allFlights, upstream.Data.List...)
		if len(upstream.Data.List) < query.PageSize {
			break
		}
	}

	response := normalizeResponse(direction, lang, query, allFlights)
	slog.Info("fetched CAN flights from upstream", "direction", direction, "total", response.Total)
	c.storeCachedResponse(ctx, cacheKey, response)
	return response, nil
}

func (c *Client) fetchUpstream(ctx context.Context, query FlightRequest) (UpstreamListResponse, error) {
	bodyBytes, err := json.Marshal(query)
	if err != nil {
		return UpstreamListResponse{}, fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, flightsURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return UpstreamListResponse{}, fmt.Errorf("build upstream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json;charset=utf-8")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", baseURL+"/departure-flight")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("locale", "Cn")

	if c.privateKey != nil {
		ts := fmt.Sprintf("%d", time.Now().UnixMilli())
		nonce := uuid.New().String()
		sortedBody := sortJSONKeys(string(bodyBytes))
		message := "Timestamp&" + ts + "&" + "/byairport-flight/flight/list" + "&" + nonce + "&" + sortedBody
		sig, signErr := signMessage(c.privateKey, message)
		if signErr != nil {
			return UpstreamListResponse{}, fmt.Errorf("sign request: %w", signErr)
		}
		req.Header.Set("Signature", sig)
		req.Header.Set("Timestamp", ts)
		req.Header.Set("Nonce", nonce)
	}

	slog.Info("requesting baiyunairport flight upstream", "url", flightsURL, "page", query.PageNum)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return UpstreamListResponse{}, fmt.Errorf("request upstream: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return UpstreamListResponse{}, fmt.Errorf("read upstream response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return UpstreamListResponse{}, fmt.Errorf("upstream request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var payload UpstreamListResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return UpstreamListResponse{}, fmt.Errorf("decode upstream response: %w", err)
	}

	if payload.Code != "200" {
		return UpstreamListResponse{}, fmt.Errorf("upstream returned code %s: %s", payload.Code, payload.Msg)
	}

	return payload, nil
}

func normalizeResponse(direction string, lang string, query FlightRequest, upstreamFlights []UpstreamFlight) Response {
	flights := make([]Flight, 0, len(upstreamFlights))
	isCn := lang == "cn" || lang == ""

	for _, item := range upstreamFlights {
		flightNumbers := []string{item.FlightNo}
		logos := make([]string, 0)

		for _, cf := range item.CarouselFLights {
			if cf.Logo != "" {
				logos = append(logos, resolveLogoURL(cf.Logo))
			}
		}

		for _, sfNo := range item.ShareFlight {
			if sfNo != "" {
				for _, fn := range strings.Fields(sfNo) {
					flightNumbers = append(flightNumbers, fn)
				}
			}
		}

		var depAirport, arrAirport string
		if isCn {
			depAirport = item.OrgCityCn
			arrAirport = item.DstCityCn
		} else {
			depAirport = item.OrgCityEn
			arrAirport = item.DstCityEn
		}

		var statusText string
		if isCn {
			statusText = item.FlightStatusCn
		} else {
			statusText = item.FlightStatusEn
		}

		flights = append(flights, Flight{
			FlightNumbers:    flightNumbers,
			AirlineLogos:     logos,
			PlannedDeparture: formatTime(item.SetoffTimePlan),
			PlannedArrival:   formatTime(item.ArriTimePlan),
			ActualDeparture:  formatTime(item.SetoffTimeAct),
			ActualArrival:    formatTime(item.ArriTimeAct),
			DepartureAirport: depAirport,
			ArrivalAirport:   arrAirport,
			Terminal:         item.Terminal,
			Gate:             item.BoardingGate,
			GateDescription:  "",
			BaggageBelt:      item.BaggageTable,
			CheckInArea:      item.CheckInCounter,
			CheckInWindow:    "",
			StatusText:       statusText,
			StatusCode:       "",
			AircraftType:     item.PlaneModle,
			Raw:              item,
		})
	}

	return Response{
		Source:    "baiyunairport",
		Direction: direction,
		Query:     query,
		Total:     len(flights),
		Flights:   flights,
	}
}

func formatTime(datetime string) string {
	if datetime == "" {
		return ""
	}
	parts := strings.SplitN(datetime, " ", 2)
	if len(parts) == 2 {
		return parts[1][:5]
	}
	return datetime
}

func directionToDepOrArr(direction string) (string, error) {
	switch direction {
	case "departure":
		return "1", nil
	case "arrival":
		return "2", nil
	default:
		return "", errors.New("direction must be departure or arrival")
	}
}

func resolveLogoURL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return baseURL + path
}

func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("key is not RSA")
	}
	return rsaKey, nil
}

func signMessage(key *rsa.PrivateKey, message string) (string, error) {
	hashed := sha256.Sum256([]byte(message))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// sortJSONKeys re-serializes a JSON object string with keys sorted alphabetically.
func sortJSONKeys(jsonStr string) string {
	if jsonStr == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return jsonStr
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sorted := make(map[string]any, len(keys))
	for _, k := range keys {
		if m[k] != nil {
			sorted[k] = m[k]
		}
	}
	out, err := json.Marshal(sorted)
	if err != nil {
		return jsonStr
	}
	return string(out)
}

func newRedisCache() responseCache {
	client := bredis.GetClient(defaultRedisKey)
	if client == nil {
		slog.Warn("CAN flights cache disabled: redis client not configured", "redis_key", defaultRedisKey)
		return nil
	}
	slog.Info("CAN flights cache enabled", "redis_key", defaultRedisKey, "ttl", defaultFlightsCacheTTL)
	return &redisCache{client: client}
}

func (c *redisCache) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, key).Result()
}

func (c *redisCache) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

func flightsCacheKey(direction string, lang string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s", direction, lang)))
	return fmt.Sprintf("%s%x", flightsCachePrefix, sum)
}

func (c *Client) loadCachedResponse(ctx context.Context, cacheKey string) (Response, bool) {
	if c.cache == nil {
		return Response{}, false
	}
	value, err := c.cache.Get(ctx, cacheKey)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Warn("failed to load cached CAN flights response", "key", cacheKey, "error", err)
		}
		return Response{}, false
	}
	var response Response
	if err := json.Unmarshal([]byte(value), &response); err != nil {
		slog.Warn("failed to decode cached CAN flights response", "key", cacheKey, "error", err)
		return Response{}, false
	}
	return response, true
}

func (c *Client) storeCachedResponse(ctx context.Context, cacheKey string, response Response) {
	if c.cache == nil {
		return
	}
	payload, err := json.Marshal(response)
	if err != nil {
		slog.Warn("failed to encode CAN flights response for cache", "error", err)
		return
	}
	if err := c.cache.Set(ctx, cacheKey, string(payload), c.cacheTTL); err != nil {
		slog.Warn("failed to store CAN flights response in cache", "key", cacheKey, "error", err)
		return
	}
	slog.Info("stored CAN flights response in cache", "key", cacheKey, "ttl", c.cacheTTL, "total", response.Total)
}

func (c *Client) FetchDailyFlights(ctx context.Context, direction string) ([]byte, error) {
	response, err := c.Fetch(ctx, direction, "cn")
	if err != nil {
		return nil, fmt.Errorf("fetch CAN daily flights: %w", err)
	}
	return json.Marshal(response)
}
