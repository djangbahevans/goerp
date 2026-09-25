import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../http/index.js";
import { ActionRegistry, actionRegistry } from "../react/action-registry.js";
import { createActionMutationOptions } from "../react/use-action.js";
import type { MetaSchema } from "../schema/types.js";
import { callAction, callActionWith } from "./call-action.js";

const schema: MetaSchema = {
  engine_version: "test",
  schema_hash: "abc",
  modules: {
    sales: {
      name: "sales",
      version: "1.0.0",
      display_name: "Sales",
      routes: [
        { method: "POST", path: "/orders/{id}/confirm", name: "confirm", permissions: [], response_is_list: false },
        { method: "POST", path: "/orders/bulk_import", name: "bulkImport", permissions: [], response_is_list: false },
      ],
      views: [],
      navigation: [],
      models: {},
      permissions: [],
      frontend: null,
      view_extensions: [],
      view_extension_definitions: [],
      load_order: 0,
      public_config: {},
    },
  },
};

function fakeClient() {
  return {
    get: vi.fn(async () => "get"),
    post: vi.fn(async () => "post"),
    put: vi.fn(async () => "put"),
    patch: vi.fn(async () => "patch"),
    delete: vi.fn(async () => "delete"),
  };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("callActionWith", () => {
  const registry = new ActionRegistry({ getSchema: async () => schema });

  it.each([
    ["a record-scope action", "sales.confirm", { id: "o1", body: { note: "rush" } }],
    ["a collection-scope action", "sales.bulkImport", { rows: 3 }],
  ])("sends %s to the same method and path useAction does", async (_, routeName, variables) => {
    const direct = fakeClient();
    const viaHook = fakeClient();

    await callActionWith(registry, direct as never, routeName, variables);
    const opts = createActionMutationOptions(routeName, {}, new QueryClient(), registry, viaHook as never);
    await opts.mutationFn!(variables, undefined as never);

    expect(direct.post.mock.calls).toEqual(viaHook.post.mock.calls);
    expect(direct.post).toHaveBeenCalledTimes(1);
  });

  it("fills a record-scope path from the variables and sends the rest as the body", async () => {
    const client = fakeClient();
    await callActionWith(registry, client as never, "sales.confirm", { id: "o1", body: { note: "rush" } });
    expect(client.post).toHaveBeenCalledWith("/orders/o1/confirm", { note: "rush" });
  });

  it("sends a collection-scope action's variables as the body unchanged", async () => {
    const client = fakeClient();
    await callActionWith(registry, client as never, "sales.bulkImport", { rows: 3 });
    expect(client.post).toHaveBeenCalledWith("/orders/bulk_import", { rows: 3 });
  });

  it("rejects an unknown action", async () => {
    await expect(callActionWith(registry, fakeClient() as never, "sales.missing", undefined)).rejects.toThrow(
      /no action named "missing"/,
    );
  });
});

describe("callAction", () => {
  it("resolves through the shared action registry and calls the shared apiClient", async () => {
    vi.spyOn(actionRegistry, "resolve").mockResolvedValue({ method: "POST", path: "/orders/{id}/confirm" });
    const post = vi.spyOn(apiClient, "post").mockResolvedValue({ id: "o1", state: "confirmed" });

    await expect(callAction("sales.confirm", "o1")).resolves.toEqual({ id: "o1", state: "confirmed" });
    expect(post).toHaveBeenCalledWith("/orders/o1/confirm", undefined);
  });
});
