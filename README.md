# KAPI

Minimal Go/Gin API service with Shenzhen Airport flight proxy endpoints.

## Quick Start

1. Install dependencies:

   ```bash
   make tidy
   ```

2. Run the service:

   ```bash
   make run
   ```

3. Verify endpoints:

   ```bash
   curl http://localhost:8080/health
   curl http://localhost:8080/ready
   curl http://localhost:8080/api/v1/ping
   curl 'http://localhost:8080/api/v1/szx/departures?flightNo=CZ5387'
   curl 'http://localhost:8080/api/v1/szx/arrivals?flightNo=CA1303'
   ```

## Files

- `cmd/service`: application entrypoint
- `internal/config`: custom config struct
- `internal/http`: HTTP route registration
- `internal/szx`: Shenzhen Airport upstream client and response normalization
- `config.yaml`: local file-based config
- `Makefile`: common build and run targets
- `Dockerfile`: multi-stage container build

## SZX API

- `GET /api/v1/szx/departures`
- `GET /api/v1/szx/arrivals`

Supported query parameters:

- `type`: `cn` or `en`, default `cn`
- `currentDate`: upstream date selector, default `1`
- `currentTime`: upstream time selector, default `8`
- `flightNo`: optional filter, mapped to upstream `hbxx_hbh`

The response includes the original upstream payload in `raw` and a normalized `flights` array for easier consumption.
