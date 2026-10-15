import type { APIClient } from "@goerp/sdk";
import type { ModelDef, ResourceRegistry, ResourceRegistryEntry } from "@goerp/sdk/schema";
import { simulateRealtimeMessage } from "@goerp/sdk/testing";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Row } from "../list/list-view-types.js";
import {
  createFormDefaults,
  createRecordQueryOptions,
  recordQueryKey,
  saveRecord,
  useFormRecord,
} from "./use-form-record.js";

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
      pivotPath: null,
      listMethod: "GET",
      createMethod: "POST",
      updateMethod: "PUT",
      deleteMethod: null,
      createPermissions: null,
      updatePermissions: null,
      deletePermissions: null,
      previewPath: null,
      ...entry,
    })),
  };
}

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

  it("rejects instead of POSTing to an empty path when the resource declares no create route", async () => {
    const post = vi.fn();
    const client = { post, put: vi.fn(), patch: vi.fn() } as unknown as Pick<APIClient, "post" | "put" | "patch">;

    await expect(
      saveRecord("contacts.contact", undefined, { name: "Acme" }, fakeRegistry({ createPath: "" }), client),
    ).rejects.toThrow(/declares no create route/);
    expect(post).not.toHaveBeenCalled();
  });

  it("rejects instead of PUTing to an empty path when the resource declares no update route", async () => {
    const put = vi.fn();
    const client = { post: vi.fn(), put, patch: vi.fn() } as unknown as Pick<APIClient, "post" | "put" | "patch">;

    await expect(
      saveRecord("contacts.contact", "01j", { name: "Acme" }, fakeRegistry({ updatePath: "" }), client),
    ).rejects.toThrow(/declares no update route/);
    expect(put).not.toHaveBeenCalled();
  });
});

// The hook itself, exercised end-to-end through its own injectable
// registry/client seam (UseFormRecordOptions) rather than mocking
// @goerp/sdk — the same seam form-renderer.stories.tsx would need to drive
// a real save mutation without a live backend.
function contactModel(): ModelDef {
  return {
    name: "contacts.contact",
    label: "Contact",
    label_plural: "Contacts",
    enabled_ops: [],
    shareable: false,
    fields: [
      { name: "id", type: "uuid", primary_key: true, readonly: true, has_default: true },
      { name: "type", type: "selection", required: true, has_default: true, default: "company" },
      { name: "name", type: "char", required: true },
      { name: "is_active", type: "boolean", has_default: true, default: false },
      { name: "created_at", type: "timestamptz", readonly: true, has_default: true, default: "ignored" },
      { name: "opened_at", type: "timestamptz", has_default: true },
    ],
  };
}

describe("createFormDefaults", () => {
  it("takes each field's literal default, including false, and skips engine-set and computed ones", () => {
    expect(createFormDefaults(contactModel())).toEqual({ type: "company", is_active: false });
  });
});

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
    result.current.save();
    await waitFor(() => expect(result.current.isSaving).toBe(true));

    resolvePut({ id: "01j", name: "Acme" });
    await waitFor(() => expect(result.current.isSaving).toBe(false));
    expect(result.current.isDirty).toBe(false);
  });

  it("save(): a rejected mutation surfaces saveError, never a rejection, and leaves the edit dirty", async () => {
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
    expect(result.current.save()).toBeUndefined();

    await waitFor(() => expect(result.current.saveError?.message).toBe("conflict"));
    expect(result.current.isDirty).toBe(true);
  });

  describe("a create form's defaults", () => {
    const models = { resolve: vi.fn(async () => contactModel()) };

    it("starts from the model's defaults without counting them as edits", async () => {
      const { result } = renderHook(
        () => useFormRecord("contacts.contact", undefined, { registry: fakeRegistry(), client: fakeClient(), models }),
        { wrapper: wrapper() },
      );

      await waitFor(() => expect(result.current.isLoading).toBe(false));
      expect(result.current.record).toEqual({ type: "company", is_active: false });
      expect(result.current.isDirty).toBe(false);
    });

    it("lets the user's choice replace a default and saves only what the user set", async () => {
      const post = vi.fn(async () => ({ id: "01new" }));
      const { result } = renderHook(
        () =>
          useFormRecord("contacts.contact", undefined, {
            registry: fakeRegistry(),
            client: fakeClient({ post }),
            models,
          }),
        { wrapper: wrapper() },
      );
      await waitFor(() => expect(result.current.isLoading).toBe(false));

      result.current.setField({ type: "person", name: "Ama" });
      await waitFor(() => expect(result.current.record).toMatchObject({ type: "person", name: "Ama" }));
      result.current.save();

      await waitFor(() =>
        expect(post).toHaveBeenCalledWith(
          "/contacts",
          { type: "person", name: "Ama" },
          { signal: expect.any(AbortSignal) },
        ),
      );
    });

    it("leaves fields without a default empty", async () => {
      const { result } = renderHook(
        () => useFormRecord("contacts.contact", undefined, { registry: fakeRegistry(), client: fakeClient(), models }),
        { wrapper: wrapper() },
      );

      await waitFor(() => expect(result.current.isLoading).toBe(false));
      expect(result.current.record).not.toHaveProperty("name");
    });

    it("does not apply defaults to an existing record", async () => {
      const client = fakeClient({ get: vi.fn(async () => ({ id: "01j", type: "person" })) });
      const resolve = vi.fn(async () => contactModel());
      const { result } = renderHook(
        () => useFormRecord("contacts.contact", "01j", { registry: fakeRegistry(), client, models: { resolve } }),
        { wrapper: wrapper() },
      );

      await waitFor(() => expect(result.current.record).toEqual({ id: "01j", type: "person" }));
      expect(resolve).not.toHaveBeenCalled();
    });

    it("opens an empty form when the model cannot be resolved", async () => {
      const failing = { resolve: vi.fn(async () => Promise.reject(new Error("unknown resource"))) };
      const { result } = renderHook(
        () =>
          useFormRecord("contacts.contact", undefined, {
            registry: fakeRegistry(),
            client: fakeClient(),
            models: failing,
          }),
        { wrapper: wrapper() },
      );

      await waitFor(() => expect(result.current.isLoading).toBe(false));
      expect(result.current.record).toEqual({});
    });
  });
});

describe("form record unavailability", () => {
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });
  const terminal = () =>
    simulateRealtimeMessage("record:contacts.contact:01j", {
      type: "record.unavailable",
      resource: "contacts.contact",
      recordId: "01j",
    });

  function setup(overrides: Record<string, unknown> = {}, autoSave = false) {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const client = {
      get: vi.fn(async () => ({ id: "01j", name: "Server name", private_value: "Hidden after terminal" })),
      put: vi.fn(async () => ({ id: "01j" })),
      post: vi.fn(),
      patch: vi.fn(),
      ...overrides,
    } as unknown as Pick<APIClient, "get" | "post" | "put" | "patch">;
    const onSaved = vi.fn();
    const hook = renderHook(
      () =>
        useFormRecord("contacts.contact", "01j", {
          registry: fakeRegistry(),
          client,
          onSaved,
          autoSave,
          autoSaveDelay: 100,
        }),
      {
        wrapper: ({ children }: { children: ReactNode }) => (
          <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
        ),
      },
    );
    return { ...hook, queryClient, client, onSaved };
  }

  it("keeps only unsaved edit values and prevents saves and refetches", async () => {
    const { result, queryClient, client } = setup();
    await waitFor(() => expect(result.current.record.name).toBe("Server name"));
    act(() => result.current.setField({ name: "Local edit" }));
    act(terminal);
    expect(result.current.isUnavailable).toBe(true);
    expect(result.current.record).toEqual({});
    expect(result.current.unsavedEdits).toEqual({ name: "Local edit" });
    expect(queryClient.getQueryData(recordQueryKey("contacts.contact", "01j"))).toBeUndefined();
    act(() => {
      result.current.save();
      result.current.refetch();
    });
    expect(client.put).not.toHaveBeenCalled();
    expect(client.get).toHaveBeenCalledTimes(1);
    act(() => result.current.reset());
    expect(result.current.unsavedEdits).toEqual({});
    expect(result.current.isUnavailable).toBe(true);
  });

  it("prevents a late save response from restoring cache or discarding local edits", async () => {
    let finish!: (record: Row) => void;
    const put = vi.fn(
      () =>
        new Promise<Row>((resolve) => {
          finish = resolve;
        }),
    );
    const { result, queryClient, onSaved } = setup({ put });
    await waitFor(() => expect(result.current.record.name).toBe("Server name"));
    act(() => result.current.setField({ name: "Local edit" }));
    act(() => result.current.save());
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    act(terminal);
    await act(async () => {
      finish({ id: "01j", name: "Late response" });
    });
    await waitFor(() => expect(result.current.isSaving).toBe(false));
    expect(result.current.record).toEqual({});
    expect(result.current.unsavedEdits).toEqual({ name: "Local edit" });
    expect(queryClient.getQueryData(recordQueryKey("contacts.contact", "01j"))).toBeUndefined();
    expect(onSaved).not.toHaveBeenCalled();
  });

  it("stops pending autosave and preserves edits", async () => {
    const { result, client } = setup({}, true);
    await waitFor(() => expect(result.current.record.name).toBe("Server name"));
    vi.useFakeTimers();
    act(() => result.current.setField({ name: "Local edit" }));
    act(terminal);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(client.put).not.toHaveBeenCalled();
    expect(result.current.unsavedEdits).toEqual({ name: "Local edit" });
  });
});

describe("form record remote changes", () => {
  afterEach(() => {
    cleanup();
  });
  const loaded = { id: "01j", name: "Acme", email: "a@acme.test" };

  function setup() {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const get = vi.fn(async () => loaded as Row);
    const client = { get, put: vi.fn(), post: vi.fn(), patch: vi.fn() } as unknown as Pick<
      APIClient,
      "get" | "post" | "put" | "patch"
    >;
    const hook = renderHook(() => useFormRecord("contacts.contact", "01j", { registry: fakeRegistry(), client }), {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      ),
    });
    const remoteSave = async (next: Row) => {
      get.mockResolvedValue(next);
      act(() =>
        simulateRealtimeMessage("record:contacts.contact:01j", {
          type: "record.updated",
          resource: "contacts.contact",
          recordId: "01j",
        }),
      );
      await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
    };
    return { ...hook, remoteSave };
  }

  it("flags a remote save to an edited field and loads the saved version on resolve", async () => {
    const { result, remoteSave } = setup();
    await waitFor(() => expect(result.current.record).toEqual(loaded));
    act(() => result.current.setField({ name: "Local" }));

    await remoteSave({ ...loaded, name: "Remote" });

    await waitFor(() => expect(result.current.hasConflict).toBe(true));
    expect(result.current.record.name).toBe("Local");
    act(() => result.current.resolveConflict());
    expect(result.current.record).toEqual({ ...loaded, name: "Remote" });
    expect(result.current.isDirty).toBe(false);
    expect(result.current.hasConflict).toBe(false);
  });

  it("keeps the local edit when the user chooses to", async () => {
    const { result, remoteSave } = setup();
    await waitFor(() => expect(result.current.record).toEqual(loaded));
    act(() => result.current.setField({ name: "Local" }));
    await remoteSave({ ...loaded, name: "Remote" });
    await waitFor(() => expect(result.current.hasConflict).toBe(true));

    act(() => result.current.keepLocalEdits());

    expect(result.current.hasConflict).toBe(false);
    expect(result.current.unsavedEdits).toEqual({ name: "Local" });
  });

  it("applies a remote save to an untouched field without a conflict", async () => {
    const { result, remoteSave } = setup();
    await waitFor(() => expect(result.current.record).toEqual(loaded));
    act(() => result.current.setField({ name: "Local" }));

    await remoteSave({ ...loaded, email: "b@acme.test" });

    await waitFor(() => expect(result.current.record.email).toBe("b@acme.test"));
    expect(result.current.record.name).toBe("Local");
    expect(result.current.hasConflict).toBe(false);
  });
});
