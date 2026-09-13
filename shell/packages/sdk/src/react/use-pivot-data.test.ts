import { describe, expect, it, vi } from "vitest";
import type { APIClient } from "../http/types.js";
import type { ResourceRegistry, ResourceRegistryEntry } from "../schema/index.js";
import { createPivotDataQueryOptions, type PivotResponse } from "./use-pivot-data.js";

function fakeRegistry(entry: Partial<ResourceRegistryEntry> = {}): Pick<ResourceRegistry, "resolve"> {
  return {
    resolve: vi.fn(async () => ({
      module: "sales",
      resource: "sales.order",
      listPath: "/orders",
      getPath: "/orders/{id}",
      createPath: "/orders",
      updatePath: "/orders/{id}",
      deletePath: null,
      pivotPath: "/orders/pivot",
      listMethod: "GET",
      createMethod: "POST",
      updateMethod: "PUT",
      deleteMethod: null,
      ...entry,
    })),
  };
}

function fakeClient(response: PivotResponse): Pick<APIClient, "get"> {
  const get = vi.fn(async () => response);
  return { get } as unknown as Pick<APIClient, "get">;
}

async function callQueryFn(options: ReturnType<typeof createPivotDataQueryOptions>): Promise<PivotResponse> {
  const fn = options.queryFn as () => Promise<PivotResponse>;
  return fn();
}

describe("createPivotDataQueryOptions", () => {
  it("resolves the resource's pivot path and sends rows/columns/values as query params", async () => {
    const registry = fakeRegistry();
    const client = fakeClient({ cells: [] });
    const options = createPivotDataQueryOptions(
      "sales.order",
      {
        rows: ["customer_name"],
        columns: ["state"],
        values: [
          { field: "amount_total", aggregation: "sum" },
          { field: "id", aggregation: "count" },
        ],
        filter: { confirmed: true },
      },
      registry,
      client,
    );

    await callQueryFn(options);

    expect(registry.resolve).toHaveBeenCalledWith("sales.order");
    expect(client.get).toHaveBeenCalledWith("/orders/pivot", {
      params: {
        rows: "customer_name",
        columns: "state",
        values: "amount_total:sum,id:count",
        "filter[confirmed]": true,
      },
    });
  });

  it("omits rows/columns params when empty", async () => {
    const registry = fakeRegistry();
    const client = fakeClient({ cells: [] });
    const options = createPivotDataQueryOptions(
      "sales.order",
      { rows: [], columns: ["state"], values: [{ field: "id", aggregation: "count" }] },
      registry,
      client,
    );

    await callQueryFn(options);

    expect(client.get).toHaveBeenCalledWith("/orders/pivot", {
      params: { columns: "state", values: "id:count" },
    });
  });

  it("rejects when the resource declares no pivot route", async () => {
    const registry = fakeRegistry({ pivotPath: null });
    const options = createPivotDataQueryOptions(
      "sales.order",
      { rows: ["region"], columns: [], values: [{ field: "id", aggregation: "count" }] },
      registry,
      fakeClient({ cells: [] }),
    );

    await expect(callQueryFn(options)).rejects.toThrow(/declares no pivot route/);
  });

  it("is disabled when both rows and columns are empty", () => {
    const options = createPivotDataQueryOptions(
      "sales.order",
      { rows: [], columns: [], values: [{ field: "id", aggregation: "count" }] },
      fakeRegistry(),
      fakeClient({ cells: [] }),
    );

    expect(options.enabled).toBe(false);
  });
});
