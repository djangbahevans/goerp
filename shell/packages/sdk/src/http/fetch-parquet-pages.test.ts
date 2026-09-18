import { describe, expect, it, vi } from "vitest";
import { fetchAllParquetPages } from "./fetch-parquet-pages.js";
import type { APIClient } from "./types.js";

function blobOf(bytes: number[]): Blob {
  return new Blob([new Uint8Array(bytes)]);
}

describe("fetchAllParquetPages", () => {
  it("sends format=parquet and the Accept header on the first request", async () => {
    const getBlobWithHeaders = vi.fn(async () => ({
      blob: blobOf([1]),
      headers: new Headers({ "X-Has-More": "false" }),
    }));
    const client = { getBlobWithHeaders } as unknown as Pick<APIClient, "getBlobWithHeaders">;

    await fetchAllParquetPages("/sales/orders", { filter: { confirmed: true } }, client);

    expect(getBlobWithHeaders).toHaveBeenCalledWith("/sales/orders", {
      params: { format: "parquet", "filter[confirmed]": true },
      headers: { Accept: "application/vnd.apache.parquet" },
    });
  });

  it("stops after one page when X-Has-More is false", async () => {
    const getBlobWithHeaders = vi.fn(async () => ({
      blob: blobOf([1, 2, 3]),
      headers: new Headers({ "X-Has-More": "false" }),
    }));
    const client = { getBlobWithHeaders } as unknown as Pick<APIClient, "getBlobWithHeaders">;

    const pages = await fetchAllParquetPages("/sales/orders", {}, client);

    expect(getBlobWithHeaders).toHaveBeenCalledTimes(1);
    expect(pages).toEqual([new Uint8Array([1, 2, 3])]);
  });

  it("loops the cursor across pages until X-Has-More is false", async () => {
    const getBlobWithHeaders = vi
      .fn()
      .mockResolvedValueOnce({
        blob: blobOf([1]),
        headers: new Headers({ "X-Has-More": "true", "X-Next-Cursor": "c1" }),
      })
      .mockResolvedValueOnce({
        blob: blobOf([2]),
        headers: new Headers({ "X-Has-More": "true", "X-Next-Cursor": "c2" }),
      })
      .mockResolvedValueOnce({ blob: blobOf([3]), headers: new Headers({ "X-Has-More": "false" }) });
    const client = { getBlobWithHeaders } as unknown as Pick<APIClient, "getBlobWithHeaders">;

    const pages = await fetchAllParquetPages("/sales/orders", {}, client);

    expect(pages).toEqual([new Uint8Array([1]), new Uint8Array([2]), new Uint8Array([3])]);
    expect(getBlobWithHeaders).toHaveBeenNthCalledWith(
      2,
      "/sales/orders",
      expect.objectContaining({ params: expect.objectContaining({ cursor: "c1" }) }),
    );
    expect(getBlobWithHeaders).toHaveBeenNthCalledWith(
      3,
      "/sales/orders",
      expect.objectContaining({ params: expect.objectContaining({ cursor: "c2" }) }),
    );
  });

  it("passes the abort signal through to every page request", async () => {
    const controller = new AbortController();
    const getBlobWithHeaders = vi.fn(async () => ({
      blob: blobOf([]),
      headers: new Headers({ "X-Has-More": "false" }),
    }));
    const client = { getBlobWithHeaders } as unknown as Pick<APIClient, "getBlobWithHeaders">;

    await fetchAllParquetPages("/sales/orders", { signal: controller.signal }, client);

    expect(getBlobWithHeaders).toHaveBeenCalledWith(
      "/sales/orders",
      expect.objectContaining({ signal: controller.signal }),
    );
  });
});
