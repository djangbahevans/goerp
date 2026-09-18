import type { FilterParamValue } from "./filter-params.js";
import { flattenFilterParams } from "./filter-params.js";
import type { APIClient } from "./types.js";

// view-system.md §8's use_wasm:true contract.
const PARQUET_ACCEPT = "application/vnd.apache.parquet";

export interface FetchParquetPagesOptions {
  filter?: Record<string, FilterParamValue>;
  signal?: AbortSignal;
}

// Pages through a list route's ?format=parquet response until exhausted.
// The endpoint caps each page at the same row limit as its JSON
// counterpart, and the body is a raw Parquet file with no JSON envelope —
// so unlike useInfiniteList's UI-driven paging, the only way to assemble
// "the full filtered dataset" is to loop the X-Next-Cursor/X-Has-More
// response headers here, page by page, before returning anything.
export async function fetchAllParquetPages(
  listPath: string,
  options: FetchParquetPagesOptions,
  client: Pick<APIClient, "getBlobWithHeaders">,
): Promise<Uint8Array[]> {
  const pages: Uint8Array[] = [];
  let cursor: string | undefined;

  do {
    const { blob, headers } = await client.getBlobWithHeaders(listPath, {
      params: {
        format: "parquet",
        ...flattenFilterParams(options.filter),
        ...(cursor !== undefined ? { cursor } : {}),
      },
      headers: { Accept: PARQUET_ACCEPT },
      ...(options.signal !== undefined ? { signal: options.signal } : {}),
    });
    pages.push(new Uint8Array(await blob.arrayBuffer()));
    cursor = headers.get("X-Has-More") === "true" ? (headers.get("X-Next-Cursor") ?? undefined) : undefined;
  } while (cursor !== undefined && cursor !== "");

  return pages;
}
