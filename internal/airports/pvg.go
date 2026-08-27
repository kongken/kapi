package airports

import (
	"context"

	"github.com/kongken/kapi/internal/pvg"
)

// PVGProvider adapts the Shanghai Pudong airport client to the v2 provider
// interface. It supports the domestic/international (zone) split natively:
// the upstream site serves domestic and international through distinct
// direction codes, which the client maps for us.
type PVGProvider struct {
	client *pvg.Client
}

func NewPVGProvider(httpClient pvg.HTTPDoer) *PVGProvider {
	return &PVGProvider{client: pvg.NewClient(httpClient)}
}

func (p *PVGProvider) Code() string {
	return "pvg"
}

func (p *PVGProvider) Info() AirportInfo {
	return AirportInfo{
		Code:       "pvg",
		NameCn:     "上海浦东国际机场",
		NameEn:     "Shanghai Pudong International Airport",
		City:       "上海",
		HasWeather: false,
		Zones:      []string{ZoneDomestic, ZoneInternational},
	}
}

func (p *PVGProvider) GetFlights(ctx context.Context, query FlightQuery) (FlightsResponse, error) {
	response, err := p.client.Fetch(ctx, query.Direction, query.Zone, query.Date, query.FlightNo)
	if err != nil {
		return FlightsResponse{}, err
	}

	items := make([]Flight, 0, len(response.Flights))
	for _, flight := range response.Flights {
		items = append(items, Flight{
			FlightNumbers:        flight.FlightNumbers,
			AirlineLogos:         flight.AirlineLogos,
			PlannedDepartureTime: flight.PlannedDeparture,
			PlannedArrivalTime:   flight.PlannedArrival,
			ActualDepartureTime:  flight.ActualDeparture,
			ActualArrivalTime:    flight.ActualArrival,
			DepartureAirport:     flight.DepartureAirport,
			ArrivalAirport:       flight.ArrivalAirport,
			Terminal:             flight.Terminal,
			Gate:                 flight.Gate,
			GateDescription:      flight.GateDescription,
			BaggageBelt:          flight.BaggageBelt,
			CheckInArea:          flight.CheckInArea,
			CheckInWindow:        flight.CheckInWindow,
			StatusText:           flight.StatusText,
			StatusCode:           flight.StatusCode,
			AircraftType:         flight.AircraftType,
			Zone:                 flight.Zone,
			Raw:                  flight.Raw,
		})
	}

	return FlightsResponse{
		Source:    response.Source,
		Airport:   p.Code(),
		Resource:  "flights",
		Direction: query.Direction,
		Query: FlightQuery{
			Direction: query.Direction,
			Lang:      query.Lang,
			Date:      query.Date,
			Time:      query.Time,
			FlightNo:  query.FlightNo,
			Zone:      query.Zone,
		},
		Total: len(items),
		Items: items,
		Raw:   response.Raw,
	}, nil
}

func (p *PVGProvider) GetWeather(ctx context.Context) (WeatherResponse, error) {
	return WeatherResponse{
		Source:   "shairport",
		Airport:  p.Code(),
		Resource: "weather",
		Total:    0,
		Items:    []Weather{},
	}, nil
}
