import { runSZXCollection, type Env } from "./collector";

async function executeScheduledCollection(env: Env): Promise<void> {
  const results = await runSZXCollection(env);
  for (const result of results) {
    const log = {
      event: "szx_collection",
      ...result,
    };
    if (result.outcome === "failed") {
      console.error(log);
    } else {
      console.log(log);
    }
  }

  const failedDirections = results
    .filter((result) => result.outcome === "failed")
    .map((result) => result.direction);
  if (failedDirections.length > 0) {
    throw new Error(
      `SZX collection failed for: ${failedDirections.join(", ")}`,
    );
  }
}

export default {
  fetch(request: Request): Response {
    const url = new URL(request.url);
    if (request.method === "GET" && url.pathname === "/health") {
      return Response.json({
        status: "healthy",
        mode: "scheduled",
      });
    }
    return Response.json(
      {
        error: "not_found",
      },
      { status: 404 },
    );
  },

  scheduled(
    _controller: ScheduledController,
    env: Env,
    context: ExecutionContext,
  ): void {
    context.waitUntil(executeScheduledCollection(env));
  },
} satisfies ExportedHandler<Env>;
