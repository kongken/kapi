# Butterfly Starter

Minimal project scaffold based on the Butterfly quick-start guide.

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
   ```

## Files

- `cmd/service`: application entrypoint
- `internal/config`: custom config struct
- `internal/http`: HTTP route registration
- `config.yaml`: local file-based config
- `Makefile`: common build and run targets
- `Dockerfile`: multi-stage container build
