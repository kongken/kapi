# SZX Collector Worker

A scheduled Cloudflare Worker that fetches complete Shenzhen Airport flight data and submits raw direction-specific batches to kapi. kapi remains responsible for validation, normalization, deduplication, and persistence.

## Behavior

Every five minutes the Worker:

1. fetches `currentTime=0..12` for departures;
2. submits the complete departure batch to kapi;
3. repeats the process for arrivals;
4. reports each direction as `accepted`, `duplicate`, `stale`, or `failed` in structured logs.

A direction is never submitted with missing pages. Requests use bounded concurrency, a 10-second timeout, and up to three attempts for network errors, HTTP `408`, `425`, `429`, and `5xx` responses.

## Configuration

`wrangler.jsonc` contains the non-secret kapi base URL:

```json
{
  "vars": {
    "KAPI_BASE_URL": "https://kapi.lovec.at"
  }
}
```

Store the shared ingestion token as a Worker secret:

```bash
npx wrangler secret put KAPI_SZX_INGEST_TOKEN
```

The same value must be mounted into kapi as the `KAPI_SZX_INGEST_TOKEN` environment variable. Never commit the value.

## Development

```bash
npm install
npm run check
npm run dev
```

The HTTP surface is intentionally read-only:

```text
GET /health
```

Collection runs only from the Cron Trigger; there is no public manual-trigger route.

## Deployment

```bash
npm run deploy
npx wrangler tail
```

Confirm that deployed Worker logs show accepted batches before changing kapi's `szx.collection_mode` from `pull` to `push`. A local Wrangler run does not prove that Cloudflare's deployed egress can reach `www.szairport.com`.
