import * as v from "valibot";
import { describe, expect, it } from "vitest";
import { optionalNullable } from "./optional-nullable.js";

describe("optionalNullable", () => {
  const schema = v.looseObject({ a: optionalNullable(v.string()) });

  it("accepts an explicit JSON null — manifest-spec.md's own examples use this for an unset optional field", () => {
    const result = v.safeParse(schema, { a: null });
    expect(result.success).toBe(true);
    expect(result.success && result.output.a).toBeUndefined();
  });

  it("accepts an omitted key", () => {
    const result = v.safeParse(schema, {});
    expect(result.success).toBe(true);
    expect(result.success && result.output.a).toBeUndefined();
  });

  it("keeps a real value", () => {
    const result = v.safeParse(schema, { a: "x" });
    expect(result.success && result.output.a).toBe("x");
  });

  it("still rejects the wrong type", () => {
    expect(v.safeParse(schema, { a: 5 }).success).toBe(false);
  });
});
