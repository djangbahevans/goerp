import { describe, expect, it } from "vitest";
import { readRecordValue, unresolvedRelationIds } from "./record-values.js";

const fields = [{ name: "customer_id", type: "many2one", read_key: "buyer" }];

describe("relation response values", () => {
  it("keeps raw IDs available when a handler omits expansion", () => {
    const rows = [
      { customer_id: "inline", buyer: { id: "inline", display_name: "Acme" } },
      { customer_id: "bare" },
      { customer_id: "hidden", buyer: null },
    ];
    expect(unresolvedRelationIds("customer_id", rows, fields)).toEqual(["bare"]);
    expect(readRecordValue("customer_id", rows[2]!, fields)).toBeNull();
  });

  it("does not infer an inline key from a field name", () => {
    expect(readRecordValue("customer_id", { customer_id: "c1", customer: { id: "c1" } }, [])).toBe("c1");
  });
});
