import { AppError } from "@goerp/sdk/error";
import type { MockRequestHandler } from "@goerp/sdk/testing";
import { defaultParseSearch } from "@tanstack/react-router";
import type { TestingFunction, ViewTestFixture } from "./fixture.js";

type MockFunction = (...args: unknown[]) => unknown;

function matchPath(template: string, path: string): Record<string, string> | undefined {
  const expected = template.split("/");
  const actual = path.split("/");
  if (expected.length !== actual.length) return undefined;
  const params: Record<string, string> = {};
  for (const [index, segment] of expected.entries()) {
    const value = actual[index] ?? "";
    if (segment.startsWith("{") && segment.endsWith("}") && value !== "") {
      params[segment.slice(1, -1)] = decodeURIComponent(value);
    } else if (segment !== value) return undefined;
  }
  return params;
}

function listParams(search: string): Record<string, unknown> {
  const query = defaultParseSearch(search);
  const raw = new URLSearchParams(search);
  const result: Record<string, unknown> = {};
  const filter: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(query)) {
    const match = /^filter\[([^\]]+)\](?:\[([^\]]+)\])?$/.exec(key);
    if (!match?.[1]) {
      if (key === "cursor") result.cursor = raw.get(key);
      else if (key === "limit") result.limit = Number(value);
      else if (key === "sort") {
        const sort = raw.get(key) ?? "";
        result.sort = sort.includes(",") ? sort.split(",") : sort;
      }
      continue;
    }
    const field = match[1];
    const op = match[2];
    if (!op || op === "eq") filter[field] = value;
    else if (op === "in") filter[field] = String(value).split(",");
    else if (op === "like") filter[field] = { like: String(value).replace(/^%|%$/g, "") };
    else
      filter[field] = {
        ...(typeof filter[field] === "object" ? filter[field] : {}),
        [op]: op === "gte" || op === "lte" ? String(value) : value,
      };
  }
  if (Object.keys(filter).length) result.filter = filter;
  return result;
}

async function argumentsFor(fn: TestingFunction, request: Request, path: Record<string, string>): Promise<unknown[]> {
  const url = new URL(request.url);
  return Promise.all(
    fn.parameters.map(async (parameter) => {
      switch (parameter.source) {
        case "path":
          return path[parameter.wire_name ?? parameter.name];
        case "body": {
          const text = await request.clone().text();
          return text ? JSON.parse(text) : undefined;
        }
        case "query":
          return url.search ? defaultParseSearch(url.search) : undefined;
        case "list_query":
          return listParams(url.search);
        case "etag":
          return request.headers.get("If-Match") ?? undefined;
      }
    }),
  );
}

export function createViewRequestHandler(fixture: ViewTestFixture, api: object = {}): MockRequestHandler {
  const routes = Object.entries(fixture.functions).sort(
    ([, a], [, b]) => (a.path.match(/\{/g)?.length ?? 0) - (b.path.match(/\{/g)?.length ?? 0),
  );
  return async (request) => {
    const url = new URL(request.url);
    const path = url.pathname.startsWith("/_m/") ? url.pathname.slice(3) : url.pathname;
    for (const [name, fn] of routes) {
      if (fn.method.toUpperCase() !== request.method) continue;
      const params = matchPath(fn.path, path);
      if (!params) continue;
      const mock = (api as Record<string, MockFunction>)[name];
      if (!mock) {
        const message = `Unmocked API request: ${request.method} ${url.pathname} (${fixture.module}.${name})`;
        console.error(message);
        return Response.json({ error: { code: "testing.unmocked_request", message } }, { status: 501 });
      }
      try {
        const value = await mock(...(await argumentsFor(fn, request, params)));
        if (fn.response === "void" || value === undefined) return new Response(null, { status: 204 });
        if (fn.response === "paged") {
          const paged = value as { data: unknown[]; meta: { cursor: string | null; hasMore: boolean; total?: number } };
          return Response.json({
            data: paged.data,
            meta: {
              cursor: paged.meta.cursor,
              has_more: paged.meta.hasMore,
              ...(paged.meta.total !== undefined ? { total: paged.meta.total } : {}),
            },
          });
        }
        return Response.json(value);
      } catch (error) {
        if (!(error instanceof AppError)) throw error;
        return Response.json(
          {
            error: {
              code: error.code,
              message: error.message,
              details: error.httpStatus === 422 ? (error.fieldErrors ?? error.details) : error.details,
              request_id: error.requestId,
              trace_id: error.traceId,
            },
          },
          { status: error.httpStatus },
        );
      }
    }
    return undefined;
  };
}
