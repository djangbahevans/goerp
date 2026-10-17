import { describe, expect, it } from "vitest";
import { readRecordValue, unresolvedRelationIds } from "./record-values.js";

const fields = [{ name: "customer_id", type: "many2one", read_key: "buyer" }];

describe("relation response values", () => {
  it("fetches labels for bare IDs and expansions without a display field", () => {
    const rows = [
      { customer_id: "inline", buyer: { id: "inline", display_name: "Acme" } },
      { customer_id: "bare" },
      { customer_id: "id-only", buyer: { id: "id-only" } },
      { customer_id: "hidden", buyer: null },
    ];
    expect(unresolvedRelationIds("customer_id", rows, fields)).toEqual(["bare", "id-only"]);
    expect(readRecordValue("customer_id", rows[3]!, fields)).toBeNull();
  });

  it("does not infer an inline key from a field name", () => {
    expect(readRecordValue("customer_id", { customer_id: "c1", customer: { id: "c1" } }, [])).toBe("c1");
  });
});
