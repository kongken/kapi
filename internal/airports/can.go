package airports

import (
	"context"

	"github.com/kongken/kapi/internal/can"
)

// CANProvider adapts the Guangzhou Baiyun airport client to the v2 provider interface.
type CANProvider struct {
	client *can.Client
}

func NewCANProvider(httpClient can.HTTPDoer) *CANProvider {
	return &CANProvider{client: can.NewClient(httpClient)}
}

func (p *CANProvider) Code() string {
	return "can"
}

func (p *CANProvider) Info() AirportInfo {
	return AirportInfo{
		Code:       "can",
		NameCn:     "广州白云国际机场",
		NameEn:     "Guangzhou Baiyun International Airport",
		City:       "广州",
		HasWeather: false,
	}
}

func (p *CANProvider) GetFlights(ctx context.Context, query FlightQuery) (FlightsResponse, error) {
	response, err := p.client.Fetch(ctx, query.Direction, query.Lang)
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
			Lang:      query.Lang,
		},
		Total: response.Total,
		Items: items,
		Raw:   response.Raw,
	}, nil
}

func (p *CANProvider) GetWeather(ctx context.Context) (WeatherResponse, error) {
	return WeatherResponse{
		Source:   "baiyunairport",
		Airport:  p.Code(),
		Resource: "weather",
		Total:    0,
		Items:    []Weather{},
	}, nil
}
