package airports

import (
	"context"

	"github.com/kongken/kapi/internal/szx"
)

// SZXProvider adapts the existing Shenzhen airport client to the v2 provider interface.
type SZXProvider struct {
	client *szx.Client
}

func NewSZXProvider(httpClient szx.HTTPDoer) *SZXProvider {
	return &SZXProvider{client: szx.NewClient(httpClient)}
}

func (p *SZXProvider) Code() string {
	return "szx"
}

func (p *SZXProvider) GetFlights(ctx context.Context, query FlightQuery) (FlightsResponse, error) {
	upstreamQuery := szx.Query{
		Type:        query.Lang,
		CurrentDate: query.Date,
		CurrentTime: query.Time,
		FlightNo:    query.FlightNo,
	}
	response, err := p.client.Fetch(ctx, query.Direction, upstreamQuery)
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
			Lang:      response.Query.Type,
			Date:      response.Query.CurrentDate,
			Time:      response.Query.CurrentTime,
			FlightNo:  response.Query.FlightNo,
		},
		Total: response.Total,
		Items: items,
		Raw:   response.Raw,
	}, nil
}

func (p *SZXProvider) GetWeather(ctx context.Context) (WeatherResponse, error) {
	response, err := p.client.FetchWeather(ctx)
	if err != nil {
		return WeatherResponse{}, err
	}

	items := make([]Weather, 0, len(response.Weathers))
	for _, weather := range response.Weathers {
		items = append(items, Weather{
			Date:    weather.Date,
			High:    weather.High,
			Low:     weather.Low,
			Type:    weather.Type,
			IconURL: weather.IconURL,
			Raw:     weather.Raw,
		})
	}

	return WeatherResponse{
		Source:   response.Source,
		Airport:  p.Code(),
		Resource: "weather",
		Total:    response.Total,
		Items:    items,
		Raw:      response.Raw,
	}, nil
}
