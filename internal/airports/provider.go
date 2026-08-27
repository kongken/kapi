package airports

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// FlightQuery is the shared v2 query model across airport providers.
type FlightQuery struct {
	Direction string `json:"direction"`
	Lang      string `json:"lang"`
	Date      string `json:"date"`
	Time      string `json:"time"`
	FlightNo  string `json:"flightNo"`
	// Zone splits domestic vs international (incl. HK/MO/TW) flights.
	// One of "domestic" or "international"; empty means both.
	// It is only honored by providers that advertise the value in AirportInfo.Zones.
	Zone string `json:"zone"`
}

// Zone values for the 国内/国际（含港澳台）flight split.
const (
	ZoneDomestic      = "domestic"
	ZoneInternational = "international"
)

// Flight is the normalized v2 flight payload.
type Flight struct {
	FlightNumbers        []string `json:"flightNumbers"`
	AirlineLogos         []string `json:"airlineLogos"`
	PlannedDepartureTime string   `json:"plannedDepartureTime"`
	PlannedArrivalTime   string   `json:"plannedArrivalTime"`
	ActualDepartureTime  string   `json:"actualDepartureTime"`
	ActualArrivalTime    string   `json:"actualArrivalTime"`
	DepartureAirport     string   `json:"departureAirport"`
	ArrivalAirport       string   `json:"arrivalAirport"`
	Terminal             string   `json:"terminal"`
	Gate                 string   `json:"gate"`
	GateDescription      string   `json:"gateDescription"`
	BaggageBelt          string   `json:"baggageBelt"`
	CheckInArea          string   `json:"checkInArea"`
	CheckInWindow        string   `json:"checkInWindow"`
	StatusText           string   `json:"statusText"`
	StatusCode           string   `json:"statusCode"`
	AircraftType         string   `json:"aircraftType"`
	// Zone classifies the flight as "domestic" or "international" (incl. HK/MO/TW).
	// Empty when the provider cannot determine it.
	Zone string `json:"zone"`
	Raw  any    `json:"raw"`
}

// FlightsResponse is the normalized v2 flights response.
type FlightsResponse struct {
	Source    string      `json:"source"`
	Airport   string      `json:"airport"`
	Resource  string      `json:"resource"`
	Direction string      `json:"direction"`
	Query     FlightQuery `json:"query"`
	Total     int         `json:"total"`
	Items     []Flight    `json:"items"`
	Raw       any         `json:"raw"`
}

// Weather is the normalized v2 weather payload.
type Weather struct {
	Date    string `json:"date"`
	High    string `json:"high"`
	Low     string `json:"low"`
	Type    string `json:"type"`
	IconURL string `json:"iconUrl"`
	Raw     any    `json:"raw"`
}

// WeatherResponse is the normalized v2 weather response.
type WeatherResponse struct {
	Source   string    `json:"source"`
	Airport  string    `json:"airport"`
	Resource string    `json:"resource"`
	Query    struct{}  `json:"query"`
	Total    int       `json:"total"`
	Items    []Weather `json:"items"`
	Raw      any       `json:"raw"`
}

// AirportInfo describes a supported airport and its capabilities.
type AirportInfo struct {
	Code       string `json:"code"`
	NameCn     string `json:"nameCn"`
	NameEn     string `json:"nameEn"`
	City       string `json:"city"`
	HasWeather bool   `json:"hasWeather"`
	// Zones lists the supported domestic/international split values
	// (ZoneDomestic, ZoneInternational). Empty means zone filtering is not
	// supported and requests carrying a zone are rejected.
	Zones []string `json:"zones"`
}

// Provider exposes the normalized airport API surface.
type Provider interface {
	Code() string
	Info() AirportInfo
	GetFlights(ctx context.Context, query FlightQuery) (FlightsResponse, error)
	GetWeather(ctx context.Context) (WeatherResponse, error)
}

// Registry resolves airport providers by code.
type Registry struct {
	providers map[string]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	registry := &Registry{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		registry.Register(provider)
	}
	return registry
}

func (r *Registry) Register(provider Provider) {
	if provider == nil {
		return
	}
	r.providers[strings.ToLower(provider.Code())] = provider
}

func (r *Registry) Get(code string) (Provider, bool) {
	provider, ok := r.providers[strings.ToLower(code)]
	return provider, ok
}

func (r *Registry) Codes() []string {
	codes := make([]string, 0, len(r.providers))
	for code := range r.providers {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

func (r *Registry) List() []AirportInfo {
	infos := make([]AirportInfo, 0, len(r.providers))
	for _, provider := range r.providers {
		infos = append(infos, provider.Info())
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Code < infos[j].Code
	})
	return infos
}

func ValidateFlightQuery(query FlightQuery) error {
	if query.Direction != "departure" && query.Direction != "arrival" {
		return fmt.Errorf("direction must be either 'departure' or 'arrival'")
	}
	if query.Lang == "" {
		query.Lang = "cn"
	}
	if query.Lang != "cn" && query.Lang != "en" {
		return fmt.Errorf("lang must be either 'cn' or 'en'")
	}
	if query.Date != "" && !isDigitsOnly(query.Date) {
		return fmt.Errorf("date must be numeric")
	}
	if query.Time != "" && !isDigitsOnly(query.Time) {
		return fmt.Errorf("time must be numeric")
	}
	if query.Zone != "" && !ValidZone(query.Zone) {
		return fmt.Errorf("zone must be either %q or %q", ZoneDomestic, ZoneInternational)
	}
	return nil
}

// ValidZone reports whether v is a supported zone filter value.
func ValidZone(v string) bool {
	return v == ZoneDomestic || v == ZoneInternational
}

// zoneMatches reports whether a flight's zone satisfies the query filter.
// An empty query zone matches everything; a flight with an unknown (empty) zone
// is dropped once a specific zone is requested, because its membership cannot
// be confirmed.
func zoneMatches(flightZone string, queryZone string) bool {
	if queryZone == "" {
		return true
	}
	return flightZone == queryZone
}

// ValidateZoneSupport returns an error when the airport does not advertise
// support for the requested zone filter. An empty zone always passes.
func ValidateZoneSupport(info AirportInfo, zone string) error {
	if zone == "" || !ValidZone(zone) {
		return nil
	}
	for _, supported := range info.Zones {
		if supported == zone {
			return nil
		}
	}
	return fmt.Errorf("zone %q is not supported for airport %q", zone, info.Code)
}

func isDigitsOnly(v string) bool {
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
