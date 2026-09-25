import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../http/index.js";
import type { ResourceRegistry, ResourceRegistryEntry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";
import {
  createResource,
  deleteResource,
  getResource,
  listResource,
  resourceApi,
  updateResource,
} from "./resource-api.js";

const entry: ResourceRegistryEntry = {
  module: "contacts",
  resource: "contacts.contact",
  listPath: "/contacts",
  getPath: "/contacts/{id}",
  createPath: "/contacts",
  updatePath: "/contacts/{id}",
  deletePath: "/contacts/{id}",
  pivotPath: null,
  listMethod: "GET",
  createMethod: "POST",
  updateMethod: "PUT",
  deleteMethod: "DELETE",
};

function fakeRegistry(overrides: Partial<ResourceRegistryEntry> = {}): Pick<ResourceRegistry, "resolve"> {
  return { resolve: vi.fn(async () => ({ ...entry, ...overrides })) };
}

interface Contact {
  id: string;
  name: string;
  is_active: boolean;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("listResource", () => {
  it("GETs the list path with flattened filter, sort, cursor and limit, and maps meta to camelCase", async () => {
    const get = vi.fn(async () => ({
      data: [{ id: "c1", name: "Ada", is_active: true }],
      meta: { cursor: "next", has_more: true, total: 3 },
    }));

    const page = await listResource<Contact>(
      "contacts.contact",
      { filter: { is_active: true, name: { like: "Ad" } }, sort: "-name", cursor: "abc", limit: 20 },
      fakeRegistry(),
      { get } as never,
    );

    expect(get).toHaveBeenCalledWith("/contacts", {
      params: { "filter[is_active]": true, "filter[name][like]": "%Ad%", sort: "-name", cursor: "abc", limit: 20 },
    });
    expect(page).toEqual({
      data: [{ id: "c1", name: "Ada", is_active: true }],
      meta: { cursor: "next", hasMore: true, total: 3 },
    });
  });

  it("sends no params when none are given, and a null cursor on the last page", async () => {
    const get = vi.fn(async () => ({ data: [], meta: { cursor: "", has_more: false } }));

    const page = await listResource("contacts.contact", undefined, fakeRegistry(), { get } as never);

    expect(get).toHaveBeenCalledWith("/contacts", { params: {} });
    expect(page.meta).toEqual({ cursor: null, hasMore: false });
  });
});

describe("getResource", () => {
  it("GETs the id-filled get path", async () => {
    const get = vi.fn(async () => ({ id: "01j" }));
    await expect(getResource("contacts.contact", "01j", fakeRegistry(), { get } as never)).resolves.toEqual({
      id: "01j",
    });
    expect(get).toHaveBeenCalledWith("/contacts/01j");
  });
});

describe("createResource", () => {
  it("POSTs the body to the create path", async () => {
    const post = vi.fn(async () => ({ id: "01j" }));
    await createResource("contacts.contact", { name: "Acme" }, fakeRegistry(), { post } as never);
    expect(post).toHaveBeenCalledWith("/contacts", { name: "Acme" });
  });

  it("rejects when the resource declares no create route", async () => {
    const post = vi.fn();
    await expect(
      createResource("contacts.contact", {}, fakeRegistry({ createPath: "" }), { post } as never),
    ).rejects.toThrow('resource "contacts.contact" declares no create route');
    expect(post).not.toHaveBeenCalled();
  });
});

describe("updateResource", () => {
  it("sends expectedEtag as If-Match", async () => {
    const put = vi.fn(async () => ({ id: "01j" }));
    await updateResource("contacts.contact", "01j", { name: "Acme" }, "etag-1", fakeRegistry(), { put } as never);
    expect(put).toHaveBeenCalledWith("/contacts/01j", { name: "Acme" }, { headers: { "If-Match": "etag-1" } });
  });

  it("sends no If-Match without expectedEtag", async () => {
    const put = vi.fn(async () => ({ id: "01j" }));
    await updateResource("contacts.contact", "01j", { name: "Acme" }, undefined, fakeRegistry(), { put } as never);
    expect(put).toHaveBeenCalledWith("/contacts/01j", { name: "Acme" });
  });

  it("PATCHes when the model declares PATCH as its update method", async () => {
    const patch = vi.fn(async () => ({ id: "01j" }));
    await updateResource(
      "contacts.contact",
      "01j",
      { name: "Acme" },
      "etag-1",
      fakeRegistry({ updateMethod: "PATCH" }),
      { patch } as never,
    );
    expect(patch).toHaveBeenCalledWith("/contacts/01j", { name: "Acme" }, { headers: { "If-Match": "etag-1" } });
  });

  it("rejects when the resource declares no update route", async () => {
    await expect(
      updateResource("contacts.contact", "01j", {}, undefined, fakeRegistry({ updatePath: "" }), {} as never),
    ).rejects.toThrow('resource "contacts.contact" declares no update route');
  });
});

describe("deleteResource", () => {
  it("DELETEs the id-filled delete path", async () => {
    const del = vi.fn(async () => undefined);
    await deleteResource("contacts.contact", "01j", fakeRegistry(), { delete: del } as never);
    expect(del).toHaveBeenCalledWith("/contacts/01j");
  });

  it("rejects when the resource declares no delete route", async () => {
    await expect(
      deleteResource("contacts.contact", "01j", fakeRegistry({ deletePath: null }), {} as never),
    ).rejects.toThrow('resource "contacts.contact" declares no delete route');
  });
});

describe("resourceApi", () => {
  it("resolves through the shared resource registry and calls the shared apiClient", async () => {
    vi.spyOn(resourceRegistry, "resolve").mockResolvedValue(entry);
    const get = vi
      .spyOn(apiClient, "get")
      .mockImplementation(async (path: string) =>
        path === "/contacts" ? { data: [], meta: { has_more: false } } : { id: "01j" },
      );
    const post = vi.spyOn(apiClient, "post").mockResolvedValue({ id: "01j" });
    const put = vi.spyOn(apiClient, "put").mockResolvedValue({ id: "01j" });
    const del = vi.spyOn(apiClient, "delete").mockResolvedValue(undefined);

    await expect(resourceApi.list<Contact>("contacts.contact", { limit: 5 })).resolves.toEqual({
      data: [],
      meta: { cursor: null, hasMore: false },
    });
    await resourceApi.get<Contact>("contacts.contact", "01j");
    await resourceApi.create<Contact>("contacts.contact", { name: "Acme" });
    await resourceApi.update<Contact>("contacts.contact", "01j", { name: "Acme" }, "etag-1");
    await resourceApi.delete("contacts.contact", "01j");

    expect(get.mock.calls).toEqual([["/contacts", { params: { limit: 5 } }], ["/contacts/01j"]]);
    expect(post).toHaveBeenCalledWith("/contacts", { name: "Acme" });
    expect(put).toHaveBeenCalledWith("/contacts/01j", { name: "Acme" }, { headers: { "If-Match": "etag-1" } });
    expect(del).toHaveBeenCalledWith("/contacts/01j");
  });
});
