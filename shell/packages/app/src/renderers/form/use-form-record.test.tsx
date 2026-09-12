import type { APIClient } from "@goerp/sdk";
import type { ResourceRegistry, ResourceRegistryEntry } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { Row } from "../list/list-view-types.js";
import { createRecordQueryOptions, recordQueryKey, saveRecord, useFormRecord } from "./use-form-record.js";

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

// The hook itself, exercised end-to-end through its own injectable
// registry/client seam (UseFormRecordOptions) rather than mocking
// @goerp/sdk — the same seam form-renderer.stories.tsx would need to drive
// a real save mutation without a live backend.
describe("useFormRecord", () => {
  function wrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return function Wrapper({ children }: { children: ReactNode }) {
      return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    };
  }

  function fakeClient(overrides: Record<string, unknown> = {}): Pick<APIClient, "get" | "post" | "put" | "patch"> {
    return {
      get: vi.fn(async () => ({ id: "01j" })),
      post: vi.fn(async () => ({ id: "01j" })),
      put: vi.fn(async () => ({ id: "01j" })),
      patch: vi.fn(async () => ({ id: "01j" })),
      ...overrides,
    } as unknown as Pick<APIClient, "get" | "post" | "put" | "patch">;
  }

  it("loads the record, then tracks local edits as dirty without mutating the loaded data", async () => {
    const registry = fakeRegistry();
    const client = fakeClient({ get: vi.fn(async () => ({ id: "01j", name: "Acme" })) });

    const { result } = renderHook(() => useFormRecord("contacts.contact", "01j", { registry, client }), {
      wrapper: wrapper(),
    });

    await waitFor(() => expect(result.current.record).toEqual({ id: "01j", name: "Acme" }));
    expect(result.current.isDirty).toBe(false);

    result.current.setField({ name: "Acme Corp" });
    await waitFor(() => expect(result.current.isDirty).toBe(true));
    expect(result.current.record).toEqual({ id: "01j", name: "Acme Corp" });
  });

  it("save(): isSaving is true while the mutation is in flight, false once it resolves", async () => {
    const registry = fakeRegistry();
    let resolvePut: (value: Row) => void = () => {};
    const put = vi.fn(() => new Promise<Row>((resolve) => (resolvePut = resolve)));
    const client = fakeClient({ put });

    const { result } = renderHook(() => useFormRecord("contacts.contact", "01j", { registry, client }), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.record).toEqual({ id: "01j" }));

    result.current.setField({ name: "Acme" });
    const savePromise = result.current.save();
    await waitFor(() => expect(result.current.isSaving).toBe(true));

    resolvePut({ id: "01j", name: "Acme" });
    await savePromise;
    await waitFor(() => expect(result.current.isSaving).toBe(false));
    expect(result.current.isDirty).toBe(false);
  });

  it("save(): a rejected mutation surfaces saveError and leaves the edit dirty", async () => {
    const registry = fakeRegistry();
    const put = vi.fn(async () => {
      throw new Error("conflict");
    });
    const client = fakeClient({ put });

    const { result } = renderHook(() => useFormRecord("contacts.contact", "01j", { registry, client }), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.record).toEqual({ id: "01j" }));

    result.current.setField({ name: "Acme" });
    await expect(result.current.save()).rejects.toThrow("conflict");

    await waitFor(() => expect(result.current.saveError?.message).toBe("conflict"));
    expect(result.current.isDirty).toBe(true);
  });
});
