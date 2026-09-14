const SZX_UPSTREAM_URL =
  "https://www.szairport.com/szjchbjk/hbcx/flightInfo";
const KAPI_INGEST_PATH = "/internal/v1/ingest/szx/flights";
const DAILY_TIME_SLOTS = 13;
const FETCH_CONCURRENCY = 3;
const MAX_UPSTREAM_BODY_BYTES = 2 * 1024 * 1024;
const REQUEST_TIMEOUT_MS = 10_000;
const MAX_ATTEMPTS = 3;

export type Direction = "departure" | "arrival";
export type SubmissionOutcome = "accepted" | "duplicate" | "stale";

export interface Env {
  KAPI_BASE_URL: string;
  KAPI_SZX_INGEST_TOKEN: string;
}

export interface DailyPage {
  currentTime: number;
  payload: Record<string, unknown>;
}

export interface SZXFlightBatch {
  schemaVersion: 1;
  runId: string;
  source: "szairport";
  airport: "szx";
  direction: Direction;
  serviceDate: string;
  collectedAt: string;
  pages: DailyPage[];
}

export interface CollectionResult {
  direction: Direction;
  durationMs: number;
  outcome: SubmissionOutcome | "failed";
  runId?: string;
  serviceDate?: string;
  error?: string;
}

interface CollectorDependencies {
  fetch: typeof fetch;
  now: () => Date;
  randomUUID: () => string;
  sleep: (milliseconds: number) => Promise<void>;
}

class CollectorRequestError extends Error {
  constructor(
    message: string,
    readonly retryable: boolean,
  ) {
    super(message);
    this.name = "CollectorRequestError";
  }
}

const directions: readonly Direction[] = ["departure", "arrival"];
const timeSlots = Array.from({ length: DAILY_TIME_SLOTS }, (_, index) => index);

export async function runSZXCollection(
  env: Env,
  dependencyOverrides: Partial<CollectorDependencies> = {},
): Promise<CollectionResult[]> {
  const ingestURL = validateEnvironment(env);
  const dependencies: CollectorDependencies = {
    fetch: globalThis.fetch.bind(globalThis),
    now: () => new Date(),
    randomUUID: () => crypto.randomUUID(),
    sleep: (milliseconds) =>
      new Promise((resolve) => setTimeout(resolve, milliseconds)),
    ...dependencyOverrides,
  };

  const results: CollectionResult[] = [];
  for (const direction of directions) {
    const startedAt = dependencies.now();
    try {
      const result = await collectDirection(
        direction,
        ingestURL,
        env.KAPI_SZX_INGEST_TOKEN,
        dependencies,
      );
      results.push({
        ...result,
        direction,
        durationMs: dependencies.now().getTime() - startedAt.getTime(),
      });
    } catch (error) {
      results.push({
        direction,
        durationMs: dependencies.now().getTime() - startedAt.getTime(),
        outcome: "failed",
        error: errorMessage(error),
      });
    }
  }

  return results;
}

async function collectDirection(
  direction: Direction,
  ingestURL: URL,
  token: string,
  dependencies: CollectorDependencies,
): Promise<Omit<CollectionResult, "direction" | "durationMs">> {
  const collectionStartedAt = dependencies.now();
  const serviceDate = shanghaiServiceDate(collectionStartedAt);
  const pages = await mapWithConcurrency(
    timeSlots,
    FETCH_CONCURRENCY,
    (currentTime) => fetchSZXPage(direction, currentTime, dependencies),
  );
  const collectedAt = dependencies.now();
  if (shanghaiServiceDate(collectedAt) !== serviceDate) {
    throw new CollectorRequestError(
      "collection crossed the Asia/Shanghai service-date boundary",
      false,
    );
  }

  const runId = dependencies.randomUUID();
  const batch: SZXFlightBatch = {
    schemaVersion: 1,
    runId,
    source: "szairport",
    airport: "szx",
    direction,
    serviceDate,
    collectedAt: collectedAt.toISOString(),
    pages,
  };
  const outcome = await submitBatch(ingestURL, token, batch, dependencies);

  return { outcome, runId, serviceDate };
}

async function fetchSZXPage(
  direction: Direction,
  currentTime: number,
  dependencies: CollectorDependencies,
): Promise<DailyPage> {
  const url = new URL(SZX_UPSTREAM_URL);
  url.searchParams.set("type", "cn");
  url.searchParams.set("flag", direction === "departure" ? "D" : "A");
  url.searchParams.set("currentDate", "1");
  url.searchParams.set("currentTime", String(currentTime));
  url.searchParams.set("hbxx_hbh", "");

  const payload = await withRetry(
    async () => {
      const response = await fetchWithTimeout(
        dependencies.fetch,
        url,
        {
          headers: {
            Accept: "application/json",
            Referer: "https://www.szairport.com/szjchbjk/hbcx/",
            "User-Agent": "kapi-szx-collector/1.0",
          },
        },
        REQUEST_TIMEOUT_MS,
      );
      if (!response.ok) {
        await response.body?.cancel();
        throw new CollectorRequestError(
          `SZX upstream returned HTTP ${response.status} for ${direction} currentTime=${currentTime}`,
          retryableStatus(response.status),
        );
      }

      const declaredLength = Number(response.headers.get("Content-Length"));
      if (
        Number.isFinite(declaredLength) &&
        declaredLength > MAX_UPSTREAM_BODY_BYTES
      ) {
        await response.body?.cancel();
        throw new CollectorRequestError(
          `SZX upstream payload exceeds ${MAX_UPSTREAM_BODY_BYTES} bytes`,
          false,
        );
      }

      const body = await response.arrayBuffer();
      if (body.byteLength > MAX_UPSTREAM_BODY_BYTES) {
        throw new CollectorRequestError(
          `SZX upstream payload exceeds ${MAX_UPSTREAM_BODY_BYTES} bytes`,
          false,
        );
      }

      let decoded: unknown;
      try {
        decoded = JSON.parse(new TextDecoder().decode(body));
      } catch (error) {
        throw new CollectorRequestError(
          `SZX upstream returned invalid JSON: ${errorMessage(error)}`,
          true,
        );
      }
      if (!isRecord(decoded) || !Array.isArray(decoded.flightList)) {
        throw new CollectorRequestError(
          "SZX upstream response is missing flightList",
          true,
        );
      }
      return decoded;
    },
    dependencies.sleep,
  );

  return { currentTime, payload };
}

async function submitBatch(
  ingestURL: URL,
  token: string,
  batch: SZXFlightBatch,
  dependencies: CollectorDependencies,
): Promise<SubmissionOutcome> {
  const body = JSON.stringify(batch);
  return withRetry(
    async () => {
      const response = await fetchWithTimeout(
        dependencies.fetch,
        ingestURL,
        {
          method: "POST",
          headers: {
            Accept: "application/json",
            Authorization: `Bearer ${token}`,
            "Content-Type": "application/json",
            "Idempotency-Key": batch.runId,
            "User-Agent": "kapi-szx-collector/1.0",
          },
          body,
        },
        REQUEST_TIMEOUT_MS,
      );

      if (response.status === 409) {
        await response.body?.cancel();
        return "stale";
      }
      if (response.status !== 200 && response.status !== 201) {
        await response.body?.cancel();
        throw new CollectorRequestError(
          `kapi ingestion returned HTTP ${response.status}`,
          retryableStatus(response.status),
        );
      }

      const receipt = await response.json<unknown>();
      if (!isRecord(receipt) || receipt.runId !== batch.runId) {
        throw new CollectorRequestError(
          "kapi ingestion returned an invalid receipt",
          true,
        );
      }
      return response.status === 200 ? "duplicate" : "accepted";
    },
    dependencies.sleep,
  );
}

async function fetchWithTimeout(
  fetcher: typeof fetch,
  input: RequestInfo | URL,
  init: RequestInit,
  timeoutMs: number,
): Promise<Response> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetcher(input, { ...init, signal: controller.signal });
  } finally {
    clearTimeout(timeout);
  }
}

async function withRetry<T>(
  operation: () => Promise<T>,
  sleep: CollectorDependencies["sleep"],
): Promise<T> {
  let lastError: unknown;
  for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt++) {
    try {
      return await operation();
    } catch (error) {
      lastError = error;
      const retryable =
        !(error instanceof CollectorRequestError) || error.retryable;
      if (!retryable || attempt === MAX_ATTEMPTS) {
        throw error;
      }
      await sleep(250 * 2 ** (attempt - 1));
    }
  }
  throw lastError;
}

async function mapWithConcurrency<Input, Output>(
  inputs: readonly Input[],
  concurrency: number,
  operation: (input: Input) => Promise<Output>,
): Promise<Output[]> {
  const results = new Array<Output>(inputs.length);
  let nextIndex = 0;
  let failed = false;
  let failure: unknown;
  const workers = Array.from(
    { length: Math.min(concurrency, inputs.length) },
    async () => {
      while (!failed && nextIndex < inputs.length) {
        const currentIndex = nextIndex++;
        try {
          results[currentIndex] = await operation(inputs[currentIndex]);
        } catch (error) {
          if (!failed) {
            failed = true;
            failure = error;
          }
        }
      }
    },
  );
  await Promise.all(workers);
  if (failed) {
    throw failure;
  }
  return results;
}

function validateEnvironment(env: Env): URL {
  if (!env.KAPI_SZX_INGEST_TOKEN?.trim()) {
    throw new Error("KAPI_SZX_INGEST_TOKEN is required");
  }

  let baseURL: URL;
  try {
    baseURL = new URL(env.KAPI_BASE_URL);
  } catch {
    throw new Error("KAPI_BASE_URL must be an absolute URL");
  }
  if (
    baseURL.protocol !== "https:" &&
    !(baseURL.protocol === "http:" &&
      ["localhost", "127.0.0.1"].includes(baseURL.hostname))
  ) {
    throw new Error("KAPI_BASE_URL must use HTTPS");
  }
  return new URL(KAPI_INGEST_PATH, baseURL);
}

export function shanghaiServiceDate(date: Date): string {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).formatToParts(date);
  const values = new Map(parts.map((part) => [part.type, part.value]));
  return `${values.get("year")}-${values.get("month")}-${values.get("day")}`;
}

function retryableStatus(status: number): boolean {
  return status === 408 || status === 425 || status === 429 || status >= 500;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
