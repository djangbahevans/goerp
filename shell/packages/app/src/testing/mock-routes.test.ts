import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";
import { afterEach, describe, expect, it, vi } from "vitest";
import fixture from "../../../../../modules/demo/frontend/src/api/generated.testing.json";
import type { ViewTestFixture } from "./fixture.js";
import { createViewRequestHandler } from "./mock-routes.js";

const parsed = fixture as ViewTestFixture;
afterEach(() => vi.restoreAllMocks());

describe("generated route mocks", () => {
  it("decodes list filters and encodes the engine list envelope", async () => {
    const listItems = vi.fn(async () => ({ data: [{ id: "1" }], meta: { cursor: "next", hasMore: true, total: 3 } }));
    const handler = createViewRequestHandler(parsed, { listItems });
    const response = await handler(
      new Request(
        "http://testing.local/demo/items?filter[name][like]=%25Ada%25&filter[id][in]=1,2&limit=10&sort=-name&cursor=start",
      ),
    );
    expect(listItems).toHaveBeenCalledWith({
      filter: { name: { like: "Ada" }, id: ["1", "2"] },
      limit: 10,
      sort: "-name",
      cursor: "start",
    });
    expect(await response?.json()).toEqual({ data: [{ id: "1" }], meta: { cursor: "next", has_more: true, total: 3 } });
  });

  it("passes decoded IDs, the body and If-Match in signature order", async () => {
    const updateItem = vi.fn(async () => ({ id: "item/1" }));
    const handler = createViewRequestHandler(parsed, { updateItem });
    await handler(
      new Request("http://testing.local/_m/demo/items/item%2F1", {
        method: "PUT",
        headers: { "If-Match": "etag-1" },
        body: JSON.stringify({ name: "Renamed" }),
      }),
    );
    expect(updateItem).toHaveBeenCalledWith("item/1", { name: "Renamed" }, "etag-1");
  });

  it("retains string cursors, multi-field sorts and range bounds", async () => {
    const listItems = vi.fn(async () => ({ data: [], meta: { cursor: null, hasMore: false } }));
    const handler = createViewRequestHandler(parsed, { listItems });
    await handler(
      new Request("http://testing.local/demo/items?cursor=123&sort=name,-id&filter[name][gte]=10&filter[name][lte]=20"),
    );
    expect(listItems).toHaveBeenCalledWith({
      cursor: "123",
      sort: ["name", "-id"],
      filter: { name: { gte: "10", lte: "20" } },
    });
  });

  it("preserves AppError validation fields and identifiers through the SDK", async () => {
    const error = new AppError({
      code: "demo.invalid",
      message: "Name required",
      httpStatus: 422,
      fieldErrors: { name: ["Required"] },
      requestId: "request-1",
      traceId: "trace-1",
    });
    const handler = createViewRequestHandler(parsed, {
      createItem: async () => {
        throw error;
      },
    });
    vi.stubGlobal("fetch", async (url: string, init: RequestInit) =>
      handler(new Request(new URL(url, "http://testing.local"), init)),
    );
    try {
      await expect(apiClient.post("/demo/items", {})).rejects.toMatchObject({
        code: "demo.invalid",
        httpStatus: 422,
        fieldErrors: { name: ["Required"] },
        requestId: "request-1",
        traceId: "trace-1",
      });
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("maps action and raw path parameters independently of their TypeScript names", async () => {
    const extended: ViewTestFixture = {
      ...parsed,
      functions: {
        mergeItems: {
          method: "POST",
          path: "/demo/items/{id}/merge",
          parameters: [
            { name: "id", source: "path", wire_name: "id" },
            { name: "body", source: "body" },
          ],
          response: "value",
        },
        getByBody: {
          method: "GET",
          path: "/demo/by-body/{body}",
          parameters: [
            { name: "bodyParam", source: "path", wire_name: "body" },
            { name: "params", source: "query" },
          ],
          response: "value",
        },
        deleteItem: {
          method: "DELETE",
          path: "/demo/items/{id}",
          parameters: [{ name: "id", source: "path", wire_name: "id" }],
          response: "void",
        },
      },
    };
    const mergeItems = vi.fn(async () => ({ merged: true }));
    const getByBody = vi.fn(async () => ({ id: "1" }));
    const handler = createViewRequestHandler(extended, { mergeItems, getByBody, deleteItem: async () => {} });
    await handler(new Request("http://testing.local/demo/items/1/merge", { method: "POST", body: '{"target":"2"}' }));
    expect(mergeItems).toHaveBeenCalledWith("1", { target: "2" });
    await handler(new Request("http://testing.local/demo/by-body/Ada%20Lovelace?enabled=true"));
    expect(getByBody).toHaveBeenCalledWith("Ada Lovelace", { enabled: true });
    expect((await handler(new Request("http://testing.local/demo/items/1", { method: "DELETE" })))?.status).toBe(204);
  });

  it("retains validation details when the mock does not provide fieldErrors", async () => {
    const handler = createViewRequestHandler(parsed, {
      createItem: async () => {
        throw new AppError({
          code: "demo.invalid",
          message: "Invalid item",
          httpStatus: 422,
          details: { name: ["Required"] },
        });
      },
    });
    const response = await handler(new Request("http://testing.local/demo/items", { method: "POST", body: "{}" }));
    expect(await response?.json()).toMatchObject({ error: { details: { name: ["Required"] } } });
  });
});
