import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import { createRecordFollowersQueryOptions, useRecordFollowers } from "./use-record-followers.js";

afterEach(() => vi.restoreAllMocks());

function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

const kwame = { user: { id: "u1", name: "Kwame Mensah", avatar_url: null }, created_at: "2026-09-23T16:02:00Z" };
const ama = { user: { id: "u2", name: null, avatar_url: "https://img" }, created_at: "2026-09-24T09:00:00Z" };

// An in-memory /_meta/activity/followers where the caller is u2.
function fakeServer(following: boolean) {
  const rows = following ? [kwame, ama] : [kwame];
  const body = { model: "sales.order", record_id: "o1" };
  const get = vi
    .spyOn(apiClient, "get")
    .mockImplementation(async () => ({ data: [...rows], meta: { following: rows.includes(ama) } }) as never);
  const put = vi.spyOn(apiClient, "put").mockImplementation(async () => {
    if (!rows.includes(ama)) rows.push(ama);
    return undefined as never;
  });
  const del = vi.spyOn(apiClient, "delete").mockImplementation(async () => {
    rows.splice(rows.indexOf(ama), 1);
    return undefined as never;
  });
  return { get, put, del, body };
}

describe("createRecordFollowersQueryOptions", () => {
  it("requests the record's followers and maps them to camelCase", async () => {
    const get = vi.fn(async () => ({ data: [kwame, ama], meta: { following: true } }));
    const { queryFn } = createRecordFollowersQueryOptions("sales.order", "o1", { get } as never);

    expect(await queryFn()).toEqual({
      followers: [
        { user: { id: "u1", name: "Kwame Mensah", avatarUrl: null }, createdAt: "2026-09-23T16:02:00Z" },
        { user: { id: "u2", name: null, avatarUrl: "https://img" }, createdAt: "2026-09-24T09:00:00Z" },
      ],
      isFollowing: true,
    });
    expect(get).toHaveBeenCalledWith("/_meta/activity/followers", {
      params: { model: "sales.order", record_id: "o1" },
    });
  });
});

describe("useRecordFollowers", () => {
  it("follows and unfollows the record, refetching the followers each time", async () => {
    const { put, del, body } = fakeServer(false);
    const { result } = renderHook(() => useRecordFollowers("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.followers).toHaveLength(1));
    expect(result.current.isFollowing).toBe(false);

    await act(() => result.current.follow());
    expect(put).toHaveBeenCalledWith("/_meta/activity/followers", body);
    await waitFor(() => expect(result.current.isFollowing).toBe(true));
    expect(result.current.followers.map((f) => f.user.id)).toEqual(["u1", "u2"]);

    await act(() => result.current.unfollow());
    expect(del).toHaveBeenCalledWith("/_meta/activity/followers", { body });
    await waitFor(() => expect(result.current.isFollowing).toBe(false));
    expect(result.current.followers.map((f) => f.user.id)).toEqual(["u1"]);
  });

  it("reports isUpdating while a follow is in flight", async () => {
    const { put } = fakeServer(false);
    let resolvePut: () => void = () => {};
    put.mockImplementationOnce(
      () =>
        new Promise<never>((resolve) => {
          resolvePut = () => resolve(undefined as never);
        }),
    );
    const { result } = renderHook(() => useRecordFollowers("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    let pending: Promise<void> = Promise.resolve();
    act(() => {
      pending = result.current.follow();
    });
    await waitFor(() => expect(result.current.isUpdating).toBe(true));
    await act(async () => {
      resolvePut();
      await pending;
    });
    await waitFor(() => expect(result.current.isUpdating).toBe(false));
  });

  it("rejects a failed follow with the AppError and leaves the followers as they were", async () => {
    const { get } = fakeServer(false);
    const failure = new AppError({ code: "permission_denied", message: "no access", httpStatus: 403 });
    vi.spyOn(apiClient, "put").mockRejectedValue(failure);
    const { result } = renderHook(() => useRecordFollowers("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.followers).toHaveLength(1));

    await expect(act(() => result.current.follow())).rejects.toBe(failure);
    expect(get).toHaveBeenCalledTimes(1);
    expect(result.current.isFollowing).toBe(false);
  });

  it("surfaces a load failure as isError with the server's AppError", async () => {
    const failure = new AppError({ code: "activity_unsupported", message: "virtual model", httpStatus: 400 });
    vi.spyOn(apiClient, "get").mockRejectedValue(failure);
    const { result } = renderHook(() => useRecordFollowers("sales.order", "o1"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBe(failure);
    expect(result.current.followers).toEqual([]);
  });
});
