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
   curl 'http://localhost:8080/api/v1/szx/departures/today'
   curl 'http://localhost:8080/api/v1/szx/arrivals/today'
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
- `GET /api/v1/szx/departures/today`
- `GET /api/v1/szx/arrivals/today`
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

The `*/today` endpoints return the latest merged all-day snapshot stored in S3. They aggregate `currentTime=0..12` for `currentDate=1` and deduplicate flights into a single daily response.

To reduce response time, the latest `*/today` snapshot is also cached in Redis when the daily sync job runs. The endpoint reads Redis first and falls back to S3 if the cache is cold.

The weather endpoint wraps the Shenzhen Airport JSONP weather interface and returns a normalized `weathers` array with date, high, low, weather text, icon URL, and raw upstream fields.

## Flight Sync Jobs

- `szx.daily_sync_interval`: all-day snapshot generation interval for the merged `today` payloads, default `30m`

The daily snapshots are stored at:

- `flights/{airport}/{direction}/daily/{YYYY-MM-DD}/latest.json`
- `flights/{airport}/{direction}/daily/{YYYY-MM-DD}/{H-M}.json`

The latest daily snapshot is also cached in Redis with keys like:

- `szx:flights:daily:{airport}:{direction}:{YYYY-MM-DD}`

The date portion uses the `Asia/Shanghai` timezone so the `today` endpoints align with Shenzhen local time.
