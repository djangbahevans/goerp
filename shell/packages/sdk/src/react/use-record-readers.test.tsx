import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import { createRecordReadersQueryOptions, recordReadersQueryKey, useRecordReaders } from "./use-record-readers.js";

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

const ama = { id: "u1", name: "Ama Owusu", email: "ama@acme.example", avatar_url: null };
const kwame = { id: "u2", name: "Kwame Mensah", email: "kwame@acme.example", avatar_url: "https://x/a.png" };

// Runs the debounce and lets the resolved request settle.
async function settle(ms = 300) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

describe("createRecordReadersQueryOptions", () => {
  it("sends only the options given and maps the wire shape to camelCase", async () => {
    const get = vi.fn(async () => ({ data: [kwame] }));
    const { queryFn } = createRecordReadersQueryOptions("sales.order", "o1", "kw", {}, { get } as never);

    const readers = await queryFn();

    expect(get).toHaveBeenCalledWith("/_meta/record-readers", {
      params: { model: "sales.order", record_id: "o1", q: "kw" },
    });
    expect(readers).toEqual([
      { id: "u2", name: "Kwame Mensah", email: "kwame@acme.example", avatarUrl: "https://x/a.png" },
    ]);
  });

  it("passes exclude_self and limit when set", async () => {
    const get = vi.fn(async () => ({ data: [] }));
    const { queryFn } = createRecordReadersQueryOptions("sales.order", "o1", "", { excludeSelf: true, limit: 5 }, {
      get,
    } as never);
    await queryFn();
    expect(get).toHaveBeenCalledWith("/_meta/record-readers", {
      params: { model: "sales.order", record_id: "o1", q: "", exclude_self: true, limit: 5 },
    });
  });
});

describe("recordReadersQueryKey", () => {
  it("differs between a mention list and an assignee picker on the same record", () => {
    const mention = recordReadersQueryKey("sales.order", "o1", "a", { excludeSelf: true });
    const assignee = recordReadersQueryKey("sales.order", "o1", "a", {});
    expect(mention).not.toEqual(assignee);
    expect(recordReadersQueryKey("sales.order", "o1", "a", { limit: 8 })).not.toEqual(assignee);
  });
});

describe("useRecordReaders", () => {
  it("does nothing while the query is null", async () => {
    const get = vi.spyOn(apiClient, "get");
    const { result } = renderHook(() => useRecordReaders("sales.order", "o1", null), { wrapper: wrapper() });
    await settle(1000);
    expect(get).not.toHaveBeenCalled();
    expect(result.current).toEqual({ readers: [], isLoading: false, isError: false, error: null });
  });

  it("debounces the query by 300ms", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({ data: [ama] });
    const { result, rerender } = renderHook(({ q }) => useRecordReaders("sales.order", "o1", q), {
      wrapper: wrapper(),
      initialProps: { q: null as string | null },
    });
    rerender({ q: "a" });
    expect(result.current.isLoading).toBe(true);
    await settle(200);
    rerender({ q: "am" });
    await settle(200);
    expect(get).not.toHaveBeenCalled();
    await settle(100);
    await settle(0);

    expect(get).toHaveBeenCalledTimes(1);
    expect(get).toHaveBeenCalledWith("/_meta/record-readers", {
      params: { model: "sales.order", record_id: "o1", q: "am" },
    });
    expect(result.current.readers.map((r) => r.id)).toEqual(["u1"]);
    expect(result.current.isLoading).toBe(false);
  });

  it("keeps the previous results while the next query loads", async () => {
    let resolveNext: (v: unknown) => void = () => {};
    vi.spyOn(apiClient, "get")
      .mockResolvedValueOnce({ data: [ama, kwame] })
      .mockImplementationOnce(() => new Promise((resolve) => (resolveNext = resolve)));
    const { result, rerender } = renderHook(({ q }) => useRecordReaders("sales.order", "o1", q), {
      wrapper: wrapper(),
      initialProps: { q: "" },
    });
    await settle();
    expect(result.current.readers).toHaveLength(2);

    rerender({ q: "k" });
    await settle();
    expect(result.current.readers).toHaveLength(2);
    expect(result.current.isLoading).toBe(true);

    resolveNext({ data: [kwame] });
    await settle(0);
    expect(result.current.readers.map((r) => r.id)).toEqual(["u2"]);
    expect(result.current.isLoading).toBe(false);
  });

  it("never shows another record's readers while loading", async () => {
    vi.spyOn(apiClient, "get")
      .mockResolvedValueOnce({ data: [ama] })
      .mockImplementationOnce(() => new Promise(() => {}));
    const { result, rerender } = renderHook(({ id }) => useRecordReaders("sales.order", id, ""), {
      wrapper: wrapper(),
      initialProps: { id: "o1" },
    });
    await settle();
    expect(result.current.readers).toHaveLength(1);

    rerender({ id: "o2" });
    await settle();
    expect(result.current.readers).toEqual([]);
    expect(result.current.isLoading).toBe(true);
  });

  it("reuses a cached result within 30 seconds", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({ data: [ama] });
    const { rerender } = renderHook(({ q }) => useRecordReaders("sales.order", "o1", q), {
      wrapper: wrapper(),
      initialProps: { q: "a" },
    });
    await settle();
    rerender({ q: "am" });
    await settle();
    rerender({ q: "a" });
    await settle();
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("surfaces a failure as isError with the AppError", async () => {
    const failure = new AppError({ code: "permission_denied", message: "no access", httpStatus: 403 });
    vi.spyOn(apiClient, "get").mockRejectedValue(failure);
    const { result } = renderHook(() => useRecordReaders("sales.order", "o1", "a"), { wrapper: wrapper() });
    await settle();
    expect(result.current.isError).toBe(true);
    expect(result.current.error).toBe(failure);
  });
});
