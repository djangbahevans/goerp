export type MockRequestHandler = (request: Request) => Promise<Response | undefined>;

export function createMockFetch(apiMocks: Record<string, unknown>, handler?: MockRequestHandler): typeof fetch {
  const patterns = Object.entries(apiMocks).map(([pattern, body]) => {
    const match = /^(\S+) (\/[^?]*)$/.exec(pattern);
    const method = match?.[1];
    const path = match?.[2];
    if (!method || !path) throw new Error(`Invalid API mock pattern: ${pattern}; expected METHOD /path`);
    const segments = path.split("/");
    return { method: method.toUpperCase(), segments, body };
  });
  patterns.sort((a, b) => a.segments.filter((s) => s === "*").length - b.segments.filter((s) => s === "*").length);

  return async (input, init) => {
    const request = typeof Request !== "undefined" && input instanceof Request ? input : undefined;
    const signal = init?.signal ?? request?.signal;
    signal?.throwIfAborted();
    const url = request?.url ?? String(input);
    const path = new URL(url, "http://testing.local").pathname;
    const method = (init?.method ?? request?.method ?? "GET").toUpperCase();
    const paths = [path, path.startsWith("/_m/") ? path.slice(3) : path];
    const mock = patterns.find(
      (entry) =>
        entry.method === method &&
        paths.some((candidate) => {
          const segments = candidate.split("/");
          return (
            segments.length === entry.segments.length &&
            entry.segments.every((segment, index) =>
              segment === "*" ? segments[index] !== "" : segment === segments[index],
            )
          );
        }),
    );

    if (!mock) {
      if (handler) {
        const response = await handler(
          request ? new Request(request, init) : new Request(new URL(url, "http://testing.local"), init),
        );
        signal?.throwIfAborted();
        if (response) return response;
      }
      const message = `Unmocked API request: ${method} ${path}`;
      console.error(message);
      return Response.json({ error: { code: "testing.unmocked_request", message } }, { status: 501 });
    }
    if (mock.body === null) {
      return Response.json({ error: { code: "not_found", message: `Not found: ${method} ${path}` } }, { status: 404 });
    }
    return new Response(JSON.stringify(mock.body) ?? null, { headers: { "Content-Type": "application/json" } });
  };
}
