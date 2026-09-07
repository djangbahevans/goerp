import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { APIClient } from "../http/types.js";
import type { ResourceRegistry, ResourceRegistryEntry } from "../schema/index.js";
import { createRecordQueryOptions, deleteRecord, recordQueryKey, saveRecord, useRecord } from "./use-record.js";

function fakeRegistry(entry: Partial<ResourceRegistryEntry> = {}): Pick<ResourceRegistry, "resolve"> {
  return {
    resolve: vi.fn(async () => ({
      module: "contacts",
      resource: "contacts.contact",
      listPath: "/contacts",
      getPath: "/contacts/{id}",
      createPath: "/contacts",
      updatePath: "/contacts/{id}",
      deletePath: "/contacts/{id}",
      listMethod: "GET",
      createMethod: "POST",
      updateMethod: "PUT",
      deleteMethod: "DELETE",
      ...entry,
    })),
  };
}

// The singleton `resourceRegistry`/`apiClient` useRecord dispatches through
// by default — mocked at module level (matching form-renderer.test.tsx's
// pattern) since, unlike the pure helper functions below, the public
// useRecord(resource, id, options) signature takes no injectable overrides.
const { resolveMock, getMock, postMock, putMock, patchMock, deleteMock } = vi.hoisted(() => ({
  resolveMock: vi.fn(async () => ({
    module: "contacts",
    resource: "contacts.contact",
    listPath: "/contacts",
    getPath: "/contacts/{id}",
    createPath: "/contacts",
    updatePath: "/contacts/{id}",
    deletePath: "/contacts/{id}",
    listMethod: "GET",
    createMethod: "POST",
    updateMethod: "PUT",
    deleteMethod: "DELETE",
  })),
  getMock: vi.fn(async () => ({ id: "01j", name: "Acme" })),
  postMock: vi.fn(),
  putMock: vi.fn(async () => ({ id: "01j", name: "Acme Updated" })),
  patchMock: vi.fn(),
  deleteMock: vi.fn(async () => undefined),
}));

vi.mock("../schema/index.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../schema/index.js")>();
  return { ...actual, resourceRegistry: { resolve: resolveMock } };
});

vi.mock("../http/index.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../http/index.js")>();
  return {
    ...actual,
    apiClient: { get: getMock, post: postMock, put: putMock, patch: patchMock, delete: deleteMock },
  };
});

describe("recordQueryKey", () => {
  it("uses null (not undefined) for a create-mode id, keeping the key JSON-stable", () => {
    expect(recordQueryKey("contacts.contact", undefined)).toEqual(["record", "contacts.contact", null]);
    expect(recordQueryKey("contacts.contact", "01j")).toEqual(["record", "contacts.contact", "01j"]);
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

  it("rejects instead of silently POSTing to an empty path when the resource declares no create route", async () => {
    const registry = fakeRegistry({ createPath: "" });
    const post = vi.fn(async () => ({}));
    const client = { post, put: vi.fn(), patch: vi.fn() } as unknown as Pick<APIClient, "post" | "put" | "patch">;

    await expect(saveRecord("contacts.contact", undefined, { name: "Acme" }, registry, client)).rejects.toThrow(
      /declares no create route/,
    );
    expect(post).not.toHaveBeenCalled();
  });

  it("rejects instead of silently PUTing to an empty path when the resource declares no update route", async () => {
    const registry = fakeRegistry({ updatePath: "" });
    const put = vi.fn(async () => ({}));
    const client = { post: vi.fn(), put, patch: vi.fn() } as unknown as Pick<APIClient, "post" | "put" | "patch">;

    await expect(saveRecord("contacts.contact", "01j", { name: "Acme" }, registry, client)).rejects.toThrow(
      /declares no update route/,
    );
    expect(put).not.toHaveBeenCalled();
  });
});

describe("deleteRecord", () => {
  it("DELETEs the id-filled deletePath", async () => {
    const registry = fakeRegistry();
    const del = vi.fn(async () => undefined);
    const client = { delete: del } as unknown as Pick<APIClient, "delete">;

    await deleteRecord("contacts.contact", "01j", registry, client);

    expect(del).toHaveBeenCalledWith("/contacts/01j");
  });

  it("rejects instead of silently no-op-ing when the resource declares no delete route", async () => {
    const registry = fakeRegistry({ deletePath: null });
    const del = vi.fn(async () => undefined);
    const client = { delete: del } as unknown as Pick<APIClient, "delete">;

    await expect(deleteRecord("contacts.contact", "01j", registry, client)).rejects.toThrow(/declares no delete route/);
    expect(del).not.toHaveBeenCalled();
  });
});

interface Contact extends Record<string, unknown> {
  id: string;
  name: string;
}

function wrapperWithClient(): { Wrapper: (props: { children: ReactNode }) => ReactNode; queryClient: QueryClient } {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return {
    Wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
    queryClient,
  };
}

function wrapper() {
  return wrapperWithClient().Wrapper;
}

describe("useRecord", () => {
  beforeEach(() => {
    resolveMock.mockClear();
    getMock.mockClear();
    postMock.mockClear();
    putMock.mockClear();
    patchMock.mockClear();
    deleteMock.mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("loads the record and starts clean (not dirty)", async () => {
    const { result } = renderHook(() => useRecord<Contact>("contacts.contact", "01j"), { wrapper: wrapper() });

    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(result.current.record).toEqual({ id: "01j", name: "Acme" });
    expect(result.current.localRecord).toEqual({ id: "01j", name: "Acme" });
    expect(result.current.isDirty).toBe(false);
  });

  it("setField updates localRecord without saving, and marks isDirty", async () => {
    const { result } = renderHook(() => useRecord<Contact>("contacts.contact", "01j"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    act(() => {
      result.current.setField("name", "New Name");
    });

    expect(result.current.localRecord).toEqual({ id: "01j", name: "New Name" });
    expect(result.current.isDirty).toBe(true);
    expect(putMock).not.toHaveBeenCalled();
  });

  it("isDirty compares against the loaded record's value, not just edit-key presence", async () => {
    const { result } = renderHook(() => useRecord<Contact>("contacts.contact", "01j"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    act(() => {
      result.current.setField("name", "Acme"); // same as the loaded value
    });

    expect(result.current.isDirty).toBe(false);
  });

  it("discard reverts localRecord to record and clears isDirty", async () => {
    const { result } = renderHook(() => useRecord<Contact>("contacts.contact", "01j"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    act(() => {
      result.current.setField("name", "New Name");
    });
    act(() => {
      result.current.discard();
    });

    expect(result.current.localRecord).toEqual({ id: "01j", name: "Acme" });
    expect(result.current.isDirty).toBe(false);
  });

  it("save() PUTs the edit buffer and clears isDirty on success", async () => {
    const { result } = renderHook(() => useRecord<Contact>("contacts.contact", "01j"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    act(() => {
      result.current.setField("name", "Acme Updated");
    });
    await act(async () => {
      await result.current.save();
    });

    expect(putMock).toHaveBeenCalledWith("/contacts/01j", { name: "Acme Updated" });
    await waitFor(() => expect(result.current.isDirty).toBe(false));
  });

  it("preserves an edit made while a save is still in flight instead of wiping it when the response arrives", async () => {
    const { result } = renderHook(() => useRecord<Contact>("contacts.contact", "01j"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    let resolvePut!: (value: Contact) => void;
    putMock.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolvePut = resolve;
        }),
    );

    act(() => {
      result.current.setField("name", "Edit A");
    });
    let savePromise!: Promise<void>;
    act(() => {
      savePromise = result.current.save();
    });
    // Let the mutation actually invoke putMock (mutate dispatch happens on
    // a later microtask, not synchronously within the act() above) before
    // simulating a second edit arriving while it's in flight.
    await waitFor(() => expect(putMock).toHaveBeenCalled());

    act(() => {
      result.current.setField("name", "Edit B");
    });

    await act(async () => {
      resolvePut({ id: "01j", name: "Edit A" });
      await savePromise;
    });

    expect(result.current.localRecord).toEqual({ id: "01j", name: "Edit B" });
    expect(result.current.isDirty).toBe(true);
  });

  it("delete() removes the record's query cache entry so a stale record isn't left rendering", async () => {
    const { Wrapper, queryClient } = wrapperWithClient();
    const removeSpy = vi.spyOn(queryClient, "removeQueries");
    const { result } = renderHook(() => useRecord<Contact>("contacts.contact", "01j"), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    await act(async () => {
      await result.current.delete();
    });

    expect(removeSpy).toHaveBeenCalledWith({ queryKey: recordQueryKey("contacts.contact", "01j") });
  });

  it("delete() calls DELETE on the id-filled deletePath", async () => {
    const { result } = renderHook(() => useRecord<Contact>("contacts.contact", "01j"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    await act(async () => {
      await result.current.delete();
    });

    expect(deleteMock).toHaveBeenCalledWith("/contacts/01j");
  });

  it("delete() rejects when there is no id (create mode)", async () => {
    const { result } = renderHook(() => useRecord<Contact>("contacts.contact", undefined), {
      wrapper: wrapper(),
    });

    await expect(result.current.delete()).rejects.toThrow(/cannot delete a record with no id/);
    expect(deleteMock).not.toHaveBeenCalled();
  });

  it("autoSave debounces a save() autoSaveDelay after the last edit", async () => {
    const { result } = renderHook(
      () => useRecord<Contact>("contacts.contact", "01j", { autoSave: true, autoSaveDelay: 1000 }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    vi.useFakeTimers();

    act(() => {
      result.current.setField("name", "First edit");
    });
    act(() => {
      vi.advanceTimersByTime(500);
    });
    act(() => {
      result.current.setField("name", "Second edit");
    });
    // The first edit's timer should have been cleared by the second edit —
    // 500ms after the second edit (1000ms total elapsed) is not yet 1000ms
    // since the second edit.
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(putMock).not.toHaveBeenCalled();

    await act(async () => {
      vi.advanceTimersByTime(500);
      await vi.runOnlyPendingTimersAsync();
    });

    expect(putMock).toHaveBeenCalledTimes(1);
    expect(putMock).toHaveBeenCalledWith("/contacts/01j", { name: "Second edit" });
  });

  it("does not autosave when autoSave is false (default)", async () => {
    const { result } = renderHook(() => useRecord<Contact>("contacts.contact", "01j"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    vi.useFakeTimers();

    act(() => {
      result.current.setField("name", "Edited");
    });
    await act(async () => {
      vi.advanceTimersByTime(5000);
      await vi.runOnlyPendingTimersAsync();
    });

    expect(putMock).not.toHaveBeenCalled();
  });
});
