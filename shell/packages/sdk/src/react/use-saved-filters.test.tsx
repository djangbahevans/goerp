import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import type { APIClient } from "../http/types.js";
import {
  createSavedFiltersQueryOptions,
  createSavedFiltersRemoveMutationOptions,
  createSavedFiltersSaveMutationOptions,
  createSavedFiltersSetDefaultMutationOptions,
  useSavedFilters,
} from "./use-saved-filters.js";

function fakeGetClient(response: unknown): Pick<APIClient, "get"> {
  const get = vi.fn(async () => response);
  return { get } as unknown as Pick<APIClient, "get">;
}

function fakeMutationClient(): Pick<APIClient, "post" | "patch" | "delete"> {
  return {
    post: vi.fn(async () => undefined),
    patch: vi.fn(async () => undefined),
    delete: vi.fn(async () => undefined),
  } as unknown as Pick<APIClient, "post" | "patch" | "delete">;
}

describe("createSavedFiltersQueryOptions", () => {
  it("fetches saved filters for the view and maps the wire shape to camelCase", async () => {
    const client = fakeGetClient({
      data: [
        {
          id: "f1",
          view_name: "contacts_list",
          label: "Active only",
          query_string: "?filter[is_active]=true",
          is_default: true,
        },
      ],
    });
    const options = createSavedFiltersQueryOptions("contacts_list", true, client);

    const result = await options.queryFn();

    expect(client.get).toHaveBeenCalledWith("/_meta/saved-filters", { params: { view_name: "contacts_list" } });
    expect(result).toEqual([
      {
        id: "f1",
        viewName: "contacts_list",
        label: "Active only",
        queryString: "?filter[is_active]=true",
        isDefault: true,
      },
    ]);
  });

  it("defaults enabled to true, and honors an explicit false", () => {
    expect(createSavedFiltersQueryOptions("contacts_list").enabled).toBe(true);
    expect(createSavedFiltersQueryOptions("contacts_list", false).enabled).toBe(false);
  });
});

describe("createSavedFiltersSaveMutationOptions", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("POSTs the view name, label, and the current location.search as query_string", async () => {
    vi.stubGlobal("location", { search: "?filter[is_active]=true&sort=-created_at" });
    const client = fakeMutationClient();
    const options = createSavedFiltersSaveMutationOptions("contacts_list", new QueryClient(), client);

    await options.mutationFn("Active only");

    expect(client.post).toHaveBeenCalledWith("/_meta/saved-filters", {
      view_name: "contacts_list",
      label: "Active only",
      query_string: "?filter[is_active]=true&sort=-created_at",
      is_default: false,
    });
  });

  it("invalidates the view's saved-filters query on success", async () => {
    const queryClient = new QueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const options = createSavedFiltersSaveMutationOptions("contacts_list", queryClient, fakeMutationClient());

    options.onSuccess();

    expect(invalidateSpy).toHaveBeenCalledExactlyOnceWith({ queryKey: ["saved-filters", "contacts_list"] });
  });
});

describe("createSavedFiltersRemoveMutationOptions", () => {
  it("DELETEs /_meta/saved-filters/{id}", async () => {
    const client = fakeMutationClient();
    const options = createSavedFiltersRemoveMutationOptions("contacts_list", new QueryClient(), client);

    await options.mutationFn("f1");

    expect(client.delete).toHaveBeenCalledWith("/_meta/saved-filters/f1");
  });
});

describe("createSavedFiltersSetDefaultMutationOptions", () => {
  it("PATCHes is_default: true", async () => {
    const client = fakeMutationClient();
    const options = createSavedFiltersSetDefaultMutationOptions("contacts_list", new QueryClient(), client);

    await options.mutationFn("f1");

    expect(client.patch).toHaveBeenCalledWith("/_meta/saved-filters/f1", { is_default: true });
  });
});

const { getMock, postMock, patchMock, deleteMock } = vi.hoisted(() => ({
  getMock: vi.fn(),
  postMock: vi.fn(),
  patchMock: vi.fn(),
  deleteMock: vi.fn(),
}));
vi.mock("../http/index.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../http/index.js")>();
  return { ...actual, apiClient: { get: getMock, post: postMock, patch: patchMock, delete: deleteMock } };
});

const { toastErrorMock } = vi.hoisted(() => ({ toastErrorMock: vi.fn() }));
vi.mock("../notifications/toast.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../notifications/toast.js")>();
  return { ...actual, toast: { ...actual.toast, error: toastErrorMock } };
});

function wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

afterEach(() => {
  getMock.mockReset();
  postMock.mockReset();
  patchMock.mockReset();
  deleteMock.mockReset();
  toastErrorMock.mockReset();
});

describe("useSavedFilters", () => {
  it("returns the view's saved filters once loaded", async () => {
    getMock.mockResolvedValue({
      data: [{ id: "f1", view_name: "contacts_list", label: "Mine", query_string: "?a=1", is_default: false }],
    });

    const { result } = renderHook(() => useSavedFilters("contacts_list"), { wrapper });

    expect(result.current.isLoading).toBe(true);
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.filters).toEqual([
      { id: "f1", viewName: "contacts_list", label: "Mine", queryString: "?a=1", isDefault: false },
    ]);
  });

  it("never fetches when enabled: false, and reports isLoading: false", () => {
    const { result } = renderHook(() => useSavedFilters("contacts_list", { enabled: false }), { wrapper });

    expect(getMock).not.toHaveBeenCalled();
    expect(result.current.isLoading).toBe(false);
    expect(result.current.filters).toEqual([]);
  });

  it("save/remove/setDefault resolve on success", async () => {
    getMock.mockResolvedValue({ data: [] });
    postMock.mockResolvedValue({ id: "f1" });
    patchMock.mockResolvedValue({ id: "f1" });
    deleteMock.mockResolvedValue(undefined);

    const { result } = renderHook(() => useSavedFilters("contacts_list"), { wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    await act(() => result.current.save("Label"));
    await act(() => result.current.setDefault("f1"));
    await act(() => result.current.remove("f1"));

    expect(postMock).toHaveBeenCalled();
    expect(patchMock).toHaveBeenCalled();
    expect(deleteMock).toHaveBeenCalled();
  });

  it("surfaces a failed mutation via toast.error and rejects the returned promise", async () => {
    getMock.mockResolvedValue({ data: [] });
    const error = new AppError({ code: "internal_error", message: "boom", httpStatus: 500 });
    postMock.mockRejectedValue(error);

    const { result } = renderHook(() => useSavedFilters("contacts_list"), { wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    await expect(act(() => result.current.save("Label"))).rejects.toThrow("boom");
    expect(toastErrorMock).toHaveBeenCalledWith("boom");
  });
});
