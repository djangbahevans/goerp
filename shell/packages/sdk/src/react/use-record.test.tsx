import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { authMachine } from "../auth/auth-machine.js";
import type { CurrentTenant, CurrentUser } from "../auth/types.js";
import type { APIClient, RequestOptions } from "../http/types.js";
import { resourceRef } from "../names.js";
import type { ResourceRegistry, ResourceRegistryEntry } from "../schema/index.js";
import { simulateRealtimeMessage } from "../testing/simulate-realtime-message.js";
import { infiniteListQueryKey } from "./use-infinite-list.js";
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
      pivotPath: null,
      listMethod: "GET",
      createMethod: "POST",
      updateMethod: "PUT",
      deleteMethod: "DELETE",
      createPermissions: null,
      updatePermissions: null,
      deletePermissions: null,
      previewPath: null,
      ...entry,
    })),
  };
}

// useRecord takes no injectable registry or client, so the singletons are mocked.
const { resolveMock, getMock, postMock, putMock, patchMock, deleteMock } = vi.hoisted(() => ({
  resolveMock: vi.fn(async () => ({
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
    createPermissions: null,
    updatePermissions: null,
    deletePermissions: null,
    previewPath: null,
  })),
  getMock: vi.fn(async (_path: string, _options?: RequestOptions) => ({ id: "01j", name: "Acme" })),
  postMock: vi.fn(),
  putMock: vi.fn(async (_path: string, _body?: unknown, _options?: RequestOptions) => ({
    id: "01j",
    name: "Acme Updated",
  })),
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
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), "01j"), {
      wrapper: wrapper(),
    });

    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(result.current.record).toEqual({ id: "01j", name: "Acme" });
    expect(result.current.localRecord).toEqual({ id: "01j", name: "Acme" });
    expect(result.current.isDirty).toBe(false);
  });

  it("setField updates localRecord without saving, and marks isDirty", async () => {
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), "01j"), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    act(() => {
      result.current.setField("name", "New Name");
    });

    expect(result.current.localRecord).toEqual({ id: "01j", name: "New Name" });
    expect(result.current.isDirty).toBe(true);
    expect(putMock).not.toHaveBeenCalled();
  });

  it("isDirty compares against the loaded record's value, not just edit-key presence", async () => {
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), "01j"), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    act(() => {
      result.current.setField("name", "Acme"); // same as the loaded value
    });

    expect(result.current.isDirty).toBe(false);
  });

  it("discard reverts localRecord to record and clears isDirty", async () => {
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), "01j"), {
      wrapper: wrapper(),
    });
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
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), "01j"), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    act(() => {
      result.current.setField("name", "Acme Updated");
    });
    await act(async () => {
      await result.current.save();
    });

    expect(putMock).toHaveBeenCalledWith(
      "/contacts/01j",
      { name: "Acme Updated" },
      { signal: expect.any(AbortSignal) },
    );
    await waitFor(() => expect(result.current.isDirty).toBe(false));
  });

  it("save() and delete() invalidate the resource's cached lists, mounted or not", async () => {
    const { Wrapper, queryClient } = wrapperWithClient();
    const listKey = infiniteListQueryKey("contacts.contact");
    const otherListKey = infiniteListQueryKey("contacts.company");
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), "01j"), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    queryClient.setQueryData(listKey, {});
    queryClient.setQueryData(otherListKey, {});
    act(() => {
      result.current.setField("name", "Acme Updated");
    });
    await act(async () => {
      await result.current.save();
    });
    expect(queryClient.getQueryState(listKey)?.isInvalidated).toBe(true);

    queryClient.setQueryData(listKey, {});
    await act(async () => {
      await result.current.delete();
    });
    expect(queryClient.getQueryState(listKey)?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(otherListKey)?.isInvalidated).toBe(false);
  });

  it("preserves an edit made while a save is still in flight instead of wiping it when the response arrives", async () => {
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), "01j"), {
      wrapper: wrapper(),
    });
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
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), "01j"), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    await act(async () => {
      await result.current.delete();
    });

    expect(removeSpy).toHaveBeenCalledWith({ queryKey: recordQueryKey("contacts.contact", "01j") });
  });

  it("delete() calls DELETE on the id-filled deletePath", async () => {
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), "01j"), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    await act(async () => {
      await result.current.delete();
    });

    expect(deleteMock).toHaveBeenCalledWith("/contacts/01j");
  });

  it("delete() rejects when there is no id (create mode)", async () => {
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), undefined), {
      wrapper: wrapper(),
    });

    await expect(result.current.delete()).rejects.toThrow(/cannot delete a record with no id/);
    expect(deleteMock).not.toHaveBeenCalled();
  });

  it("autoSave debounces a save() autoSaveDelay after the last edit", async () => {
    const { result } = renderHook(
      () => useRecord(resourceRef<Contact>("contacts.contact"), "01j", { autoSave: true, autoSaveDelay: 1000 }),
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
    expect(putMock).toHaveBeenCalledWith("/contacts/01j", { name: "Second edit" }, { signal: expect.any(AbortSignal) });
  });

  it("does not autosave when autoSave is false (default)", async () => {
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), "01j"), {
      wrapper: wrapper(),
    });
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

describe("useRecord terminal unavailability", () => {
  beforeEach(() => {
    getMock.mockReset().mockResolvedValue({ id: "01j", name: "Acme" });
    putMock.mockReset().mockResolvedValue({ id: "01j", name: "saved" });
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  function unavailable(id = "01j") {
    simulateRealtimeMessage(`record:contacts.contact:${id}`, {
      type: "record.unavailable",
      resource: "contacts.contact",
      recordId: id,
    });
  }

  it("clears the server cache, preserves only unsaved edits and blocks saving", async () => {
    const { Wrapper, queryClient } = wrapperWithClient();
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), "01j"), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(result.current.record?.name).toBe("Acme"));
    act(() => result.current.setField("name", "Local draft"));
    act(() => unavailable());
    expect(result.current.isUnavailable).toBe(true);
    expect(result.current.record).toBeUndefined();
    expect(result.current.localRecord).toBeUndefined();
    expect(result.current.unsavedEdits).toEqual({ name: "Local draft" });
    expect(queryClient.getQueryData(recordQueryKey("contacts.contact", "01j"))).toBeUndefined();
    await expect(result.current.save()).rejects.toMatchObject({ code: "record.unavailable" });
    expect(putMock).not.toHaveBeenCalled();
    act(() => result.current.discard());
    expect(result.current.unsavedEdits).toEqual({});
    expect(result.current.isUnavailable).toBe(true);
  });

  it("ignores mismatched identifiers and resets availability when navigating to another record", async () => {
    const { result, rerender } = renderHook(({ id }) => useRecord(resourceRef<Contact>("contacts.contact"), id), {
      initialProps: { id: "01j" },
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.record).toBeDefined());
    act(() =>
      simulateRealtimeMessage("record:contacts.contact:01j", {
        type: "record.unavailable",
        resource: "contacts.contact",
        recordId: "other",
      }),
    );
    expect(result.current.isUnavailable).toBe(false);
    act(() => unavailable());
    expect(result.current.isUnavailable).toBe(true);
    rerender({ id: "other" });
    await waitFor(() => expect(result.current.record).toBeDefined());
    expect(result.current.isUnavailable).toBe(false);
    expect(result.current.unsavedEdits).toEqual({});
  });

  it("cancels a queued autosave", async () => {
    const { result } = renderHook(
      () => useRecord(resourceRef<Contact>("contacts.contact"), "01j", { autoSave: true, autoSaveDelay: 100 }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.record).toBeDefined());
    vi.useFakeTimers();
    act(() => result.current.setField("name", "Local draft"));
    act(() => unavailable());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(putMock).not.toHaveBeenCalled();
    expect(result.current.unsavedEdits).toEqual({ name: "Local draft" });
  });

  it("aborts an in-flight save and ignores a late successful response", async () => {
    let finish!: (value: Contact) => void;
    putMock.mockImplementationOnce(
      () =>
        new Promise<Contact>((resolve) => {
          finish = resolve;
        }),
    );
    const { Wrapper, queryClient } = wrapperWithClient();
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), "01j"), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(result.current.record).toBeDefined());
    act(() => result.current.setField("name", "Local draft"));
    let saved!: Promise<unknown>;
    act(() => {
      saved = result.current.save().catch((error: unknown) => error);
    });
    await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
    const signal = putMock.mock.calls[0]?.[2]?.signal;
    act(() => unavailable());
    expect(signal?.aborted).toBe(true);
    await act(async () => {
      finish({ id: "01j", name: "Late server response" });
      await saved;
    });
    expect(await saved).toMatchObject({ code: "record.unavailable" });
    expect(queryClient.getQueryData(recordQueryKey("contacts.contact", "01j"))).toBeUndefined();
    expect(result.current.record).toBeUndefined();
    expect(result.current.unsavedEdits).toEqual({ name: "Local draft" });
  });

  it("cancels a refetch and ignores its late response", async () => {
    const { Wrapper, queryClient } = wrapperWithClient();
    const { result } = renderHook(() => useRecord(resourceRef<Contact>("contacts.contact"), "01j"), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(result.current.record).toBeDefined());
    let finish!: (value: Contact) => void;
    getMock.mockImplementationOnce(
      () =>
        new Promise<Contact>((resolve) => {
          finish = resolve;
        }),
    );
    void queryClient.invalidateQueries({ queryKey: recordQueryKey("contacts.contact", "01j") });
    await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
    const signal = getMock.mock.calls[1]?.[1]?.signal;
    act(() => unavailable());
    expect(signal?.aborted).toBe(true);
    await act(async () => {
      finish({ id: "01j", name: "Late server response" });
    });
    expect(queryClient.getQueryData(recordQueryKey("contacts.contact", "01j"))).toBeUndefined();
    expect(result.current.record).toBeUndefined();
    expect(result.current.isUnavailable).toBe(true);
  });
});

describe("useRecord collaboration", () => {
  type Person = { id: string; name: string; email: string };
  const person = resourceRef<Person>("contacts.contact");
  const channel = "record:contacts.contact:01j";
  const loaded: Person = { id: "01j", name: "Acme", email: "a@acme.test" };

  beforeEach(() => {
    getMock.mockReset().mockResolvedValue(loaded);
    putMock.mockReset();
  });
  afterEach(() => {
    cleanup();
  });

  async function remoteSave(next: Person) {
    getMock.mockResolvedValue(next);
    act(() =>
      simulateRealtimeMessage(channel, { type: "record.updated", resource: "contacts.contact", recordId: "01j" }),
    );
    await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
  }

  it("reports a remote save to a locally edited watched field as a conflict", async () => {
    const onRemoteChange = vi.fn();
    const { result } = renderHook(() => useRecord(person, "01j", { collaborationFields: ["name"], onRemoteChange }), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.record).toEqual(loaded));
    act(() => result.current.setField("name", "Local"));

    await remoteSave({ ...loaded, name: "Remote" });

    await waitFor(() => expect(result.current.hasConflict).toBe(true));
    expect(result.current.record).toEqual({ ...loaded, name: "Remote" });
    expect(result.current.localRecord).toEqual({ ...loaded, name: "Local" });
    expect(onRemoteChange).toHaveBeenCalledExactlyOnceWith({ ...loaded, name: "Remote" }, { ...loaded, name: "Local" });
  });

  it("applies remote saves to unedited or unwatched fields without a conflict", async () => {
    const onRemoteChange = vi.fn();
    const { result } = renderHook(() => useRecord(person, "01j", { collaborationFields: ["email"], onRemoteChange }), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.record).toEqual(loaded));
    act(() => result.current.setField("name", "Local"));

    await remoteSave({ id: "01j", name: "Remote", email: "b@acme.test" });

    await waitFor(() => expect(result.current.record?.email).toBe("b@acme.test"));
    expect(result.current.localRecord).toEqual({ id: "01j", name: "Local", email: "b@acme.test" });
    expect(result.current.hasConflict).toBe(false);
    expect(onRemoteChange).not.toHaveBeenCalled();
  });

  it("treats every edited field as watched when collaborationFields is unset", async () => {
    const onRemoteChange = vi.fn();
    const { result } = renderHook(() => useRecord(person, "01j", { onRemoteChange }), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.record).toEqual(loaded));
    act(() => result.current.setField("email", "local@acme.test"));

    await remoteSave({ ...loaded, email: "remote@acme.test" });

    await waitFor(() => expect(result.current.hasConflict).toBe(true));
    expect(onRemoteChange).toHaveBeenCalledOnce();
  });

  it("does not report a remote save that matches the local edit", async () => {
    const { result } = renderHook(() => useRecord(person, "01j"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.record).toEqual(loaded));
    act(() => result.current.setField("name", "Same"));

    await remoteSave({ ...loaded, name: "Same" });

    await waitFor(() => expect(result.current.record?.name).toBe("Same"));
    expect(result.current.hasConflict).toBe(false);
    expect(result.current.isDirty).toBe(false);
  });

  it("does not report this user's own save response as a remote change", async () => {
    putMock.mockResolvedValue({ ...loaded, name: "Normalized" });
    const onRemoteChange = vi.fn();
    const { result } = renderHook(() => useRecord(person, "01j", { onRemoteChange }), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.record).toEqual(loaded));
    act(() => result.current.setField("name", "normalized "));

    await act(() => result.current.save());

    expect(result.current.record?.name).toBe("Normalized");
    expect(result.current.hasConflict).toBe(false);
    expect(onRemoteChange).not.toHaveBeenCalled();
  });

  it("resolveConflict leaves the server version and a clean record", async () => {
    const { result } = renderHook(() => useRecord(person, "01j"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.record).toEqual(loaded));
    act(() => result.current.setFields({ name: "Local", email: "local@acme.test" }));
    const remote = { ...loaded, name: "Remote" };
    await remoteSave(remote);
    await waitFor(() => expect(result.current.hasConflict).toBe(true));

    act(() => result.current.resolveConflict());

    expect(result.current.localRecord).toEqual(remote);
    expect(result.current.record).toEqual(remote);
    expect(result.current.isDirty).toBe(false);
    expect(result.current.hasConflict).toBe(false);
  });

  it("clears the conflict when the local edits are saved over the remote version", async () => {
    putMock.mockResolvedValue({ ...loaded, name: "Local" });
    const { result } = renderHook(() => useRecord(person, "01j"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.record).toEqual(loaded));
    act(() => result.current.setField("name", "Local"));
    await remoteSave({ ...loaded, name: "Remote" });
    await waitFor(() => expect(result.current.hasConflict).toBe(true));

    await act(() => result.current.save());

    expect(putMock).toHaveBeenCalledWith("/contacts/01j", { name: "Local" }, expect.anything());
    expect(result.current.record?.name).toBe("Local");
    expect(result.current.hasConflict).toBe(false);
  });

  it("lists the other users present on the record", () => {
    const me = { id: "me", name: "Me" } as CurrentUser;
    authMachine.transition({ type: "check_session" });
    authMachine.transition({ type: "session_checked", user: me, tenant: {} as CurrentTenant });
    try {
      const { result } = renderHook(() => useRecord(person, "01j"), { wrapper: wrapper() });
      const entry = (userId: string, name: string) => ({
        user_id: userId,
        name,
        avatar_url: null,
        editing_field: userId === "ama" ? "email" : null,
        joined_at: "2026-10-14T09:00:00Z",
        last_seen_at: "2026-10-14T09:00:30Z",
      });
      act(() =>
        simulateRealtimeMessage(channel, {
          type: "custom",
          event: "presence.update",
          payload: { resource: "contacts.contact", record_id: "01j", users: [entry("me", "Me"), entry("ama", "Ama")] },
        }),
      );

      expect(result.current.collaborators).toEqual([
        {
          userId: "ama",
          name: "Ama",
          avatarUrl: null,
          editingField: "email",
          joinedAt: new Date("2026-10-14T09:00:00Z"),
          lastSeenAt: new Date("2026-10-14T09:00:30Z"),
        },
      ]);
    } finally {
      cleanup();
      authMachine.transition({ type: "logout_started" });
      authMachine.transition({ type: "logout_complete" });
    }
  });

  it("joins no presence in create mode", () => {
    const { result } = renderHook(() => useRecord(person, undefined), { wrapper: wrapper() });
    expect(result.current.collaborators).toEqual([]);
  });
});
