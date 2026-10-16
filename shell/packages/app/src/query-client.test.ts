import { AppError } from "@goerp/sdk/error";
import { createInfiniteListQueryOptions } from "@goerp/sdk/react";
import { describe, expect, it, vi } from "vitest";
import { createQueryClient, shouldRetryQuery } from "./query-client.js";
import { createRecordQueryOptions } from "./renderers/form/use-form-record.js";

function appError(httpStatus: number): AppError {
  return new AppError({ code: "test_error", message: "failed", httpStatus });
}

describe("shouldRetryQuery", () => {
  it.each([400, 401, 403, 404, 409, 422, 429])("does not retry a %i AppError", (status) => {
    expect(shouldRetryQuery(0, appError(status))).toBe(false);
  });

  it.each([500, 502, 503])("retries a %i AppError up to 3 times", (status) => {
    expect(shouldRetryQuery(0, appError(status))).toBe(true);
    expect(shouldRetryQuery(2, appError(status))).toBe(true);
    expect(shouldRetryQuery(3, appError(status))).toBe(false);
  });

  it("retries an error that is not an AppError up to 3 times", () => {
    const error = new TypeError("Failed to fetch");
    expect(shouldRetryQuery(0, error)).toBe(true);
    expect(shouldRetryQuery(2, error)).toBe(true);
    expect(shouldRetryQuery(3, error)).toBe(false);
  });
});

describe("createQueryClient", () => {
  it("fails a query on its first 4xx AppError", async () => {
    const queryClient = createQueryClient();
    const queryFn = vi.fn(async () => {
      throw appError(403);
    });

    await expect(queryClient.fetchQuery({ queryKey: ["forbidden"], queryFn, retryDelay: 0 })).rejects.toThrow();

    expect(queryFn).toHaveBeenCalledTimes(1);
  });

  it("retries a 5xx AppError 3 times before failing", async () => {
    const queryClient = createQueryClient();
    const queryFn = vi.fn(async () => {
      throw appError(503);
    });

    await expect(queryClient.fetchQuery({ queryKey: ["unavailable"], queryFn, retryDelay: 0 })).rejects.toThrow();

    expect(queryFn).toHaveBeenCalledTimes(4);
  });

  it("keeps a query without its own staleTime fresh for 30 seconds", () => {
    const queryClient = createQueryClient();

    expect(queryClient.defaultQueryOptions({ queryKey: ["anything"] }).staleTime).toBe(30_000);
    expect(queryClient.defaultQueryOptions({ queryKey: ["anything"] }).gcTime).toBe(5 * 60_000);
  });

  it("keeps the list and record freshness windows", () => {
    const queryClient = createQueryClient();
    const listKey = createInfiniteListQueryOptions("contacts.contact", {}).queryKey;
    const recordKey = createRecordQueryOptions("contacts.contact", "01abc").queryKey;

    expect(queryClient.defaultQueryOptions({ queryKey: listKey }).staleTime).toBe(5_000);
    expect(queryClient.defaultQueryOptions({ queryKey: recordKey }).staleTime).toBe(30_000);
  });
});
