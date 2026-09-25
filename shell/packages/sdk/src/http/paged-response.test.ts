import { describe, expect, it } from "vitest";
import { toPagedResponse } from "./paged-response.js";

describe("toPagedResponse", () => {
  it("maps the engine's snake_case meta to PagedResponse", () => {
    expect(toPagedResponse({ data: [1], meta: { cursor: "c1", has_more: true, total: 9 } })).toEqual({
      data: [1],
      meta: { cursor: "c1", hasMore: true, total: 9 },
    });
  });

  it("reads an empty or missing cursor and a null total as absent", () => {
    expect(toPagedResponse({ data: [], meta: { cursor: "", has_more: false, total: null } })).toEqual({
      data: [],
      meta: { cursor: null, hasMore: false },
    });
    expect(toPagedResponse({ data: [], meta: { has_more: false } }).meta.cursor).toBeNull();
  });

  it("maps each item when given a mapper", () => {
    expect(toPagedResponse({ data: [{ n: 1 }], meta: { cursor: null, has_more: false } }, (w) => w.n * 2).data).toEqual(
      [2],
    );
  });
});
