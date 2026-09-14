import * as v from "valibot";
import { describe, expect, it } from "vitest";
import { PivotViewDeclarationSchema } from "./pivot-manifest-types.js";

const base = {
  name: "sales_pivot",
  type: "pivot",
  resource: "sales.order",
  label: "Sales Analysis",
  rows: ["region"],
  columns: ["state"],
  values: [{ field: "amount_total", aggregation: "sum" }],
};

describe("PivotViewDeclarationSchema", () => {
  it("accepts a minimal valid declaration", () => {
    expect(v.safeParse(PivotViewDeclarationSchema, base).success).toBe(true);
  });

  it("requires rows/columns/values", () => {
    const { values: _values, ...withoutValues } = base;
    expect(v.safeParse(PivotViewDeclarationSchema, withoutValues).success).toBe(false);
  });

  it("rejects an aggregation outside the documented enum", () => {
    const result = v.safeParse(PivotViewDeclarationSchema, {
      ...base,
      values: [{ field: "x", aggregation: "median" }],
    });
    expect(result.success).toBe(false);
  });

  it("reuses ListFilterSchema for filters", () => {
    const result = v.safeParse(PivotViewDeclarationSchema, {
      ...base,
      filters: [{ field: "state", label: "Status", type: "select" }],
    });
    expect(result.success).toBe(true);
  });

  it("accepts an explicit null on an optional field, not just omission", () => {
    const result = v.safeParse(PivotViewDeclarationSchema, { ...base, permission: null, allow_download: null });
    expect(result.success).toBe(true);
    expect(result.success && result.output.permission).toBeUndefined();
  });
});
