import { describe, expect, it } from "vitest";

import {
  runSZXCollection,
  shanghaiServiceDate,
  type Env,
  type SZXFlightBatch,
} from "../src/collector";

const env: Env = {
  KAPI_BASE_URL: "https://kapi.example.com",
  KAPI_SZX_INGEST_TOKEN: "test-collector-token",
};

const runIDs = [
  "c91abb94-dc37-40aa-bc68-d362e5375690",
  "d0cd78ec-fd1c-4a27-b7df-c20ee2bb9646",
];

interface FakeFetchOptions {
  upstreamResponse?: (url: URL, call: number) => Response | undefined;
  ingestStatus?: number;
}

function createFakeFetch(options: FakeFetchOptions = {}) {
  const submissions: Array<{
    batch: SZXFlightBatch;
    headers: Headers;
  }> = [];
  let upstreamCalls = 0;

  const fetcher: typeof fetch = async (input, init) => {
    const url = new URL(input instanceof Request ? input.url : input.toString());
    if (url.hostname === "www.szairport.com") {
      upstreamCalls++;
      const overridden = options.upstreamResponse?.(url, upstreamCalls);
      if (overridden) {
        return overridden;
      }
      return Response.json({
        flightList: [],
        type: "cn",
        flag: url.searchParams.get("flag"),
        currentDate: 1,
        currentTime: Number(url.searchParams.get("currentTime")),
      });
    }

    if (url.pathname === "/internal/v1/ingest/szx/flights") {
      const body = typeof init?.body === "string" ? init.body : "";
      const batch = JSON.parse(body) as SZXFlightBatch;
      submissions.push({ batch, headers: new Headers(init?.headers) });
      return Response.json(
        { runId: batch.runId, duplicate: options.ingestStatus === 200 },
        { status: options.ingestStatus ?? 201 },
      );
    }

    throw new Error(`unexpected URL ${url}`);
  };

  return {
    fetcher,
    submissions,
    upstreamCallCount: () => upstreamCalls,
  };
}

function dependencies(fetcher: typeof fetch) {
  const ids = [...runIDs];
  const now = new Date("2026-09-14T06:40:00Z");
  return {
    fetch: fetcher,
    now: () => now,
    randomUUID: () => {
      const id = ids.shift();
      if (!id) {
        throw new Error("test UUIDs exhausted");
      }
      return id;
    },
    sleep: async () => {},
  };
}

describe("runSZXCollection", () => {
  it("collects and submits a complete batch for each direction", async () => {
    const fake = createFakeFetch();

    const results = await runSZXCollection(env, dependencies(fake.fetcher));

    expect(results.map((result) => result.direction)).toEqual([
      "departure",
      "arrival",
    ]);
    expect(results.map((result) => result.outcome)).toEqual([
      "accepted",
      "accepted",
    ]);
    expect(fake.upstreamCallCount()).toBe(26);
    expect(fake.submissions).toHaveLength(2);
    expect(fake.submissions.map(({ batch }) => batch.direction)).toEqual([
      "departure",
      "arrival",
    ]);
    for (const { batch, headers } of fake.submissions) {
      expect(batch.schemaVersion).toBe(1);
      expect(batch.serviceDate).toBe("2026-09-14");
      expect(batch.pages.map((page) => page.currentTime)).toEqual(
        Array.from({ length: 13 }, (_, index) => index),
      );
      expect(headers.get("Authorization")).toBe(
        "Bearer test-collector-token",
      );
      expect(headers.get("Idempotency-Key")).toBe(batch.runId);
    }
  });

  it("retries a transient upstream response", async () => {
    let failedOnce = false;
    const fake = createFakeFetch({
      upstreamResponse: (url) => {
        if (
          !failedOnce &&
          url.searchParams.get("flag") === "D" &&
          url.searchParams.get("currentTime") === "3"
        ) {
          failedOnce = true;
          return new Response(null, { status: 503 });
        }
        return undefined;
      },
    });

    const results = await runSZXCollection(env, dependencies(fake.fetcher));

    expect(results.every((result) => result.outcome === "accepted")).toBe(true);
    expect(fake.upstreamCallCount()).toBe(27);
    expect(fake.submissions).toHaveLength(2);
  });

  it("does not submit a partial direction and still processes the other one", async () => {
    const fake = createFakeFetch({
      upstreamResponse: (url) => {
        if (
          url.searchParams.get("flag") === "D" &&
          url.searchParams.get("currentTime") === "4"
        ) {
          return new Response(null, { status: 403 });
        }
        return undefined;
      },
    });

    const results = await runSZXCollection(env, dependencies(fake.fetcher));

    expect(results.map((result) => result.outcome)).toEqual([
      "failed",
      "accepted",
    ]);
    expect(fake.submissions).toHaveLength(1);
    expect(fake.submissions[0]?.batch.direction).toBe("arrival");
  });

  it("recognizes duplicate receipts", async () => {
    const fake = createFakeFetch({ ingestStatus: 200 });

    const results = await runSZXCollection(env, dependencies(fake.fetcher));

    expect(results.map((result) => result.outcome)).toEqual([
      "duplicate",
      "duplicate",
    ]);
  });

  it("treats stale submissions as completed work", async () => {
    const fake = createFakeFetch({ ingestStatus: 409 });

    const results = await runSZXCollection(env, dependencies(fake.fetcher));

    expect(results.map((result) => result.outcome)).toEqual([
      "stale",
      "stale",
    ]);
  });
});

describe("shanghaiServiceDate", () => {
  it("uses the Asia/Shanghai calendar date", () => {
    expect(shanghaiServiceDate(new Date("2026-09-13T16:00:00Z"))).toBe(
      "2026-09-14",
    );
  });
});
