// Package mcp exposes kapi airport data as Model Context Protocol (MCP) tools.
//
// The server reuses the same in-process providers (internal/airports.Registry)
// and daily snapshot loader as the REST API, so both surfaces always serve
// identical data without an extra HTTP hop.
package mcp

import (
	"context"
	"net/http"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kongken/kapi/internal/airports"
)

// SnapshotLoader loads the persisted daily flight snapshot for an airport and direction.
type SnapshotLoader interface {
	Load(ctx context.Context, airportCode string, direction string) ([]byte, error)
}

// SnapshotLoaderFunc adapts a plain function to the SnapshotLoader interface.
type SnapshotLoaderFunc func(ctx context.Context, airportCode string, direction string) ([]byte, error)

func (f SnapshotLoaderFunc) Load(ctx context.Context, airportCode string, direction string) ([]byte, error) {
	return f(ctx, airportCode, direction)
}

// Options configures the kapi MCP server.
type Options struct {
	Registry *airports.Registry
	Loader   SnapshotLoader
}

type service struct {
	registry *airports.Registry
	loader   SnapshotLoader
}

const (
	serverName    = "kapi"
	serverVersion = "0.1.0"
)

// NewServer builds an MCP server exposing airport flights, delay trends and weather as tools.
func NewServer(opts Options) *gomcp.Server {
	svc := &service{registry: opts.Registry, loader: opts.Loader}

	server := gomcp.NewServer(&gomcp.Implementation{Name: serverName, Version: serverVersion}, nil)

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_airports",
		Description: "List all airports supported by kapi, including localized names, city and capabilities such as whether weather data is available.",
	}, svc.listAirports)

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "search_flights",
		Description: "Search real-time flights at an airport. Requires airport (IATA code, e.g. 'szx' for Shenzhen Bao'an, 'can' for Guangzhou Baiyun) and direction ('departure' or 'arrival'). Optional filters: date (numeric YYYYMMDD where supported), time (numeric HHMM), flightNo, lang ('cn' default or 'en'), zone ('domestic' or 'international' — only supported by airports that advertise zones, e.g. 'can').",
	}, svc.searchFlights)

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_flight_status",
		Description: "Look up the current status of one flight by number (e.g. 'CZ3456') across departures and arrivals. Searches all supported airports unless 'airport' is given. Optional 'date' (numeric YYYYMMDD). Returns matched flights with actual times and status.",
	}, svc.getFlightStatus)

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_today_flights",
		Description: "Get today's full-day flight schedule snapshot (from the periodically synced S3/cache store) for an airport and direction. Returns status counts plus optionally filtered items (by status text or by domestic/international zone where the snapshot records it). Use this instead of search_flights when the question is about whole-day aggregates such as total cancellations.",
	}, svc.getTodayFlights)

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_delay_trend",
		Description: "Hourly delay trend (delayed/cancelled/diverted counts per hour, per direction) built from today's snapshots. Currently only 'szx' (Shenzhen Bao'an) is supported.",
	}, svc.getDelayTrend)

	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_weather",
		Description: "Get multi-day weather forecast for an airport. Currently only 'szx' (Shenzhen Bao'an) provides weather data; other airports return an empty list.",
	}, svc.getWeather)

	return server
}

// Handler returns an http.Handler implementing the MCP Streamable HTTP transport,
// ready to be mounted on the existing gin API server (e.g. at /mcp).
func Handler(opts Options) http.Handler {
	server := NewServer(opts)
	return gomcp.NewStreamableHTTPHandler(func(*http.Request) *gomcp.Server { return server }, nil)
}
