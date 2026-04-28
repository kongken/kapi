# KAPI

Minimal Go/Gin API service with Shenzhen Airport flight proxy endpoints.

## Quick Start

1. Install dependencies:

   ```bash
   make tidy
   ```

2. Generate protobuf API code:

   ```bash
   make proto
   ```

3. Run the service:

   ```bash
   make run
   ```

4. Verify endpoints:

   ```bash
   curl http://localhost:8080/health
   curl http://localhost:8080/ready
   curl http://localhost:8080/api/v1/ping
   curl 'http://localhost:8080/api/v1/szx/departures?flightNo=CZ5387'
   curl 'http://localhost:8080/api/v1/szx/arrivals?flightNo=CA1303'
   curl 'http://localhost:8080/api/v1/szx/weather'
   ```

## Files

- `cmd/service`: application entrypoint
- `internal/config`: custom config struct
- `internal/http`: HTTP route registration
- `internal/szx`: Shenzhen Airport upstream client and response normalization
- `proto`: protobuf API definitions managed by buf
- `pkg/proto`: generated protobuf, gRPC, and Connect code
- `config.yaml`: local file-based config
- `Makefile`: common build and run targets
- `Dockerfile`: multi-stage container build

## Protobuf API

This repository now uses `protobuf + buf` to manage the next-generation airport API.

- `buf.yaml`: buf module and lint/breaking rules
- `buf.gen.yaml`: Go, gRPC, and Connect code generation
- proto/airports/v2/service.proto: airport service definition
- proto/airports/v2/flight.proto: flight request and response messages
- proto/airports/v2/weather.proto: weather request and response messages

Current v2 API contract:

- `GET /api/v2/airports/{airport}/flights`
- `GET /api/v2/airports/{airport}/weather`

Common commands:

```bash
make proto
make proto-lint
```

## SZX API

- `GET /api/v1/szx/departures`
- `GET /api/v1/szx/arrivals`
- `GET /api/v1/szx/weather`

Supported query parameters:

- `type`: `cn` or `en`, default `cn`
- `currentDate`: upstream date selector, default `1`
- `currentTime`: upstream time slot selector, default `8`, validated range `0-12`
- `flightNo`: optional filter, mapped to upstream `hbxx_hbh`

Verified `currentTime` behavior against the live SZX endpoint:

- `0-11` map to the 2-hour slots starting at `00:00`, `02:00`, ..., `22:00`
- `12` is also accepted by the upstream API and returns a broader result set
- values above `12` currently make the upstream return invalid data, so `kapi` rejects them with `400 invalid_query`

The response includes the original upstream payload in `raw` and a normalized `flights` array for easier consumption.

The weather endpoint wraps the Shenzhen Airport JSONP weather interface and returns a normalized `weathers` array with date, high, low, weather text, icon URL, and raw upstream fields.
