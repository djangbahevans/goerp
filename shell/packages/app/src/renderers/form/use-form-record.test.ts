import type { APIClient } from "@goerp/sdk";
import type { ResourceRegistry, ResourceRegistryEntry } from "@goerp/sdk/schema";
import { describe, expect, it, vi } from "vitest";
import { createRecordQueryOptions, recordQueryKey, saveRecord } from "./use-form-record.js";

function fakeRegistry(entry: Partial<ResourceRegistryEntry> = {}): Pick<ResourceRegistry, "resolve"> {
  return {
    resolve: vi.fn(async () => ({
      module: "contacts",
      resource: "contacts.contact",
      listPath: "/contacts",
      getPath: "/contacts/{id}",
      createPath: "/contacts",
      updatePath: "/contacts/{id}",
      deletePath: null,
      listMethod: "GET",
      createMethod: "POST",
      updateMethod: "PUT",
      deleteMethod: null,
      ...entry,
    })),
  };
}

describe("recordQueryKey", () => {
  it("uses null (not undefined) for a create-mode id, keeping the key JSON-stable", () => {
    expect(recordQueryKey("contacts.contact", undefined)).toEqual(["form-record", "contacts.contact", null]);
    expect(recordQueryKey("contacts.contact", "01j")).toEqual(["form-record", "contacts.contact", "01j"]);
  });
});

describe("createRecordQueryOptions", () => {
  it("fetches the id-filled getPath and is disabled in create mode", async () => {
    const registry = fakeRegistry();
    const get = vi.fn(async () => ({ id: "01j", name: "Acme" }));
    const client = { get } as unknown as Pick<APIClient, "get">;
    const options = createRecordQueryOptions("contacts.contact", "01j", registry, client);

    expect(options.enabled).toBe(true);
    await options.queryFn();
    expect(get).toHaveBeenCalledWith("/contacts/01j");

    const createOptions = createRecordQueryOptions("contacts.contact", undefined, registry, client);
    expect(createOptions.enabled).toBe(false);
  });
});

describe("saveRecord", () => {
  it("POSTs to createPath when id is undefined", async () => {
    const registry = fakeRegistry();
    const post = vi.fn(async () => ({ id: "new" }));
    const client = { post, put: vi.fn(), patch: vi.fn() } as unknown as Pick<APIClient, "post" | "put" | "patch">;

    await saveRecord("contacts.contact", undefined, { name: "Acme" }, registry, client);

    expect(post).toHaveBeenCalledWith("/contacts", { name: "Acme" });
  });

  it("PUTs to the id-filled updatePath by default", async () => {
    const registry = fakeRegistry();
    const put = vi.fn(async () => ({ id: "01j" }));
    const client = { post: vi.fn(), put, patch: vi.fn() } as unknown as Pick<APIClient, "post" | "put" | "patch">;

    await saveRecord("contacts.contact", "01j", { name: "Acme" }, registry, client);

    expect(put).toHaveBeenCalledWith("/contacts/01j", { name: "Acme" });
  });

  it("PATCHes instead when the model declares PATCH as its update method", async () => {
    const registry = fakeRegistry({ updateMethod: "PATCH" });
    const patch = vi.fn(async () => ({ id: "01j" }));
    const client = { post: vi.fn(), put: vi.fn(), patch } as unknown as Pick<APIClient, "post" | "put" | "patch">;

    await saveRecord("contacts.contact", "01j", { name: "Acme" }, registry, client);

    expect(patch).toHaveBeenCalledWith("/contacts/01j", { name: "Acme" });
  });
});
