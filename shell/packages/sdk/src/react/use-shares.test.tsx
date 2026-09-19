import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import { createSharesQueryOptions, useShares } from "./use-shares.js";

afterEach(() => vi.restoreAllMocks());

function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

const wire = {
  id: "s1",
  model: "sales.order",
  record_id: "o1",
  shared_with_user_id: "u2",
  shared_with_email: "ada@example.com",
  permission: "read",
  shared_by: "u1",
  created_at: "2026-09-19T10:00:00Z",
  expires_at: "2026-10-01T00:00:00Z",
};

describe("createSharesQueryOptions", () => {
  it("fetches shares for the record and maps the wire shape to camelCase", async () => {
    const get = vi.fn(async () => ({
      data: [wire, { ...wire, id: "s2", shared_with_email: "", expires_at: undefined }],
    }));
    const { queryFn } = createSharesQueryOptions("sales.order", "o1", { get } as never);

    const shares = await queryFn();

    expect(get).toHaveBeenCalledWith("/_meta/shares", { params: { model: "sales.order", record_id: "o1" } });
    expect(shares[0]).toEqual({
      id: "s1",
      sharedWithUserId: "u2",
      sharedWithEmail: "ada@example.com",
      permission: "read",
      expiresAt: "2026-10-01T00:00:00Z",
      createdAt: "2026-09-19T10:00:00Z",
    });
    expect(shares[1]).toMatchObject({ sharedWithEmail: null, expiresAt: null });
  });
});

describe("useShares", () => {
  it("loads the record's shares", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue({ data: [wire] });
    const { result } = renderHook(() => useShares("sales.order", "o1"), { wrapper: wrapper() });
    expect(result.current.isLoading).toBe(true);
    await waitFor(() => expect(result.current.shares).toHaveLength(1));
    expect(result.current.shares[0]?.sharedWithEmail).toBe("ada@example.com");
  });

  it("surfaces a load failure as isError with the AppError", async () => {
    const failure = new AppError({ code: "permission_denied", message: "no access", httpStatus: 403 });
    vi.spyOn(apiClient, "get").mockRejectedValue(failure);
    const { result } = renderHook(() => useShares("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBe(failure);
  });

  it("grant posts the request body, including expires_at only when given, then refetches", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({ data: [] });
    const post = vi.spyOn(apiClient, "post").mockResolvedValue(wire);
    const { result } = renderHook(() => useShares("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(get).toHaveBeenCalledTimes(1));

    await act(() => result.current.grant({ userEmail: "ada@example.com", permission: "write" }));
    expect(post).toHaveBeenLastCalledWith("/_meta/shares", {
      model: "sales.order",
      record_id: "o1",
      user_email: "ada@example.com",
      permission: "write",
    });
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));

    await act(() =>
      result.current.grant({ userEmail: "b@example.com", permission: "read", expiresAt: "2026-10-01T23:59:59.999Z" }),
    );
    expect(post).toHaveBeenLastCalledWith(
      "/_meta/shares",
      expect.objectContaining({ expires_at: "2026-10-01T23:59:59.999Z" }),
    );
  });

  it("grant rejects with the engine's AppError and does not refetch", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({ data: [] });
    const failure = new AppError({ code: "recipient_not_found", message: "no user with that email", httpStatus: 400 });
    vi.spyOn(apiClient, "post").mockRejectedValue(failure);
    const { result } = renderHook(() => useShares("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(get).toHaveBeenCalledTimes(1));

    await expect(act(() => result.current.grant({ userEmail: "x@y.z", permission: "read" }))).rejects.toBe(failure);
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("revoke deletes the share and refetches", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({ data: [wire] });
    const del = vi.spyOn(apiClient, "delete").mockResolvedValue(undefined);
    const { result } = renderHook(() => useShares("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.shares).toHaveLength(1));

    await act(() => result.current.revoke("s1"));
    expect(del).toHaveBeenCalledWith("/_meta/shares/s1");
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
  });

  it("revoke treats a 404 as success", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue({ data: [wire] });
    vi.spyOn(apiClient, "delete").mockRejectedValue(
      new AppError({ code: "not_found", message: "share not found", httpStatus: 404 }),
    );
    const { result } = renderHook(() => useShares("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.shares).toHaveLength(1));

    await expect(act(() => result.current.revoke("s1"))).resolves.not.toThrow();
  });

  it("revoke rejects on any other failure", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue({ data: [wire] });
    const failure = new AppError({ code: "permission_denied", message: "no access", httpStatus: 403 });
    vi.spyOn(apiClient, "delete").mockRejectedValue(failure);
    const { result } = renderHook(() => useShares("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.shares).toHaveLength(1));

    await expect(act(() => result.current.revoke("s1"))).rejects.toBe(failure);
  });

  it("tracks every revoke in flight, not just the latest", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue({ data: [wire, { ...wire, id: "s2" }] });
    const finishers: Record<string, () => void> = {};
    vi.spyOn(apiClient, "delete").mockImplementation(
      (path: string) =>
        new Promise<undefined>((resolve) => (finishers[path.split("/").pop() as string] = () => resolve(undefined))),
    );
    const { result } = renderHook(() => useShares("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.shares).toHaveLength(2));

    const pending: Promise<void>[] = [];
    act(() => {
      pending.push(result.current.revoke("s1"));
    });
    act(() => {
      pending.push(result.current.revoke("s2"));
    });
    await waitFor(() => expect(result.current.revokingIds).toEqual(["s1", "s2"]));

    await act(async () => {
      finishers.s1?.();
      await pending[0];
    });
    await waitFor(() => expect(result.current.revokingIds).toEqual(["s2"]));

    await act(async () => {
      finishers.s2?.();
      await pending[1];
    });
    await waitFor(() => expect(result.current.revokingIds).toEqual([]));
  });

  it("stops tracking a revoke that failed", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue({ data: [wire] });
    vi.spyOn(apiClient, "delete").mockRejectedValue(
      new AppError({ code: "internal_error", message: "boom", httpStatus: 500 }),
    );
    const { result } = renderHook(() => useShares("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.shares).toHaveLength(1));

    await expect(act(() => result.current.revoke("s1"))).rejects.toThrow("boom");
    expect(result.current.revokingIds).toEqual([]);
  });
});
