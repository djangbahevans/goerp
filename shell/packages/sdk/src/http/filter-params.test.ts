import { describe, expect, it } from "vitest";
import { flattenFilterParams } from "./filter-params.js";

describe("flattenFilterParams", () => {
  it("sends a scalar value as the implicit eq", () => {
    expect(flattenFilterParams({ state: "confirmed" })).toEqual({ "filter[state]": "confirmed" });
  });

  it("sends a non-empty array joined under [in]", () => {
    expect(flattenFilterParams({ tags: ["a", "b"] })).toEqual({ "filter[tags][in]": "a,b" });
  });

  it("drops an empty array entirely", () => {
    expect(flattenFilterParams({ tags: [] })).toEqual({});
  });

  it("wraps a like value in %...% under [like]", () => {
    expect(flattenFilterParams({ name: { like: "acme" } })).toEqual({ "filter[name][like]": "%acme%" });
  });

  it("sends gte/lte as separate params", () => {
    expect(flattenFilterParams({ created_at: { gte: "2026-01-01", lte: "2026-12-31" } })).toEqual({
      "filter[created_at][gte]": "2026-01-01",
      "filter[created_at][lte]": "2026-12-31",
    });
  });

  it("sends { isnull: true } as filter[field][isnull]=true", () => {
    expect(flattenFilterParams({ parent_id: { isnull: true } })).toEqual({ "filter[parent_id][isnull]": true });
  });

  it("sends { isnull: false } as filter[field][isnull]=false", () => {
    expect(flattenFilterParams({ parent_id: { isnull: false } })).toEqual({ "filter[parent_id][isnull]": false });
  });

  it("never sends an isnull value as a gte/lte range, regardless of branch order", () => {
    // Guards against isFilterRange's own predicate matching { isnull }
    // (it excludes "like" but must also exclude "isnull" on its own terms,
    // not just by running after isFilterIsNull in this function's chain).
    expect(flattenFilterParams({ parent_id: { isnull: true } })).not.toHaveProperty("filter[parent_id][gte]");
  });

  it("returns an empty object for an undefined filter", () => {
    expect(flattenFilterParams(undefined)).toEqual({});
  });
});
