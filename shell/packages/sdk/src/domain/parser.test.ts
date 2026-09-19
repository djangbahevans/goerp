import { describe, expect, it } from "vitest";
import { DomainExpressionError } from "./errors.js";
import { parseCondition, parseValue } from "./parser.js";

function parseError(parse: () => unknown): DomainExpressionError {
  try {
    parse();
  } catch (error) {
    if (error instanceof DomainExpressionError) return error;
    throw error;
  }
  throw new Error("expected a DomainExpressionError");
}

describe("parseCondition", () => {
  it("parses record, user and literal operands", () => {
    expect(parseCondition("record.state = 'draft'")).toEqual({
      kind: "compare",
      op: "=",
      left: { kind: "record", field: "state" },
      right: { kind: "literal", value: "draft" },
    });
    expect(parseCondition("current_user.contact_id = user.id")).toEqual({
      kind: "compare",
      op: "=",
      left: { kind: "user", attr: "contact_id" },
      right: { kind: "user", attr: "id" },
    });
    expect(parseCondition("user.tenant_id")).toEqual({ kind: "user", attr: "tenant_id" });
  });

  it("parses every comparison operator", () => {
    for (const op of ["=", "!=", "<", ">", "<=", ">="]) {
      expect(parseCondition(`record.n ${op} 1`)).toMatchObject({ kind: "compare", op });
    }
  });

  it("parses role and permission checks", () => {
    expect(parseCondition("user_has_role('admin')")).toEqual({ kind: "role", role: "admin" });
    expect(parseCondition("user_has_permission('sales:order:read')")).toEqual({
      kind: "permission",
      permission: "sales:order:read",
    });
  });

  it("parses literals, IS NULL, IS NOT NULL and IN", () => {
    expect(parseCondition("true")).toEqual({ kind: "literal", value: true });
    expect(parseCondition("null")).toEqual({ kind: "literal", value: null });
    expect(parseCondition("record.x IS NULL")).toMatchObject({ kind: "isNull", negated: false });
    expect(parseCondition("record.x is not null")).toMatchObject({ kind: "isNull", negated: true });
    expect(parseCondition("record.type IN ('note', 'call')")).toEqual({
      kind: "in",
      operand: { kind: "record", field: "type" },
      values: [
        { kind: "literal", value: "note" },
        { kind: "literal", value: "call" },
      ],
    });
  });

  it("treats a doubled quote inside a string literal as an escaped quote", () => {
    expect(parseCondition("record.name = 'O''Brien'")).toMatchObject({
      right: { kind: "literal", value: "O'Brien" },
    });
  });

  it("binds AND tighter than OR", () => {
    expect(parseCondition("record.a = 1 OR record.b = 2 AND record.c = 3")).toMatchObject({
      kind: "or",
      left: { kind: "compare" },
      right: { kind: "and" },
    });
  });

  it("binds NOT below every predicate and above AND", () => {
    expect(parseCondition("NOT record.a = 1")).toMatchObject({ kind: "not", operand: { kind: "compare" } });
    expect(parseCondition("NOT record.a IS NULL")).toMatchObject({ kind: "not", operand: { kind: "isNull" } });
    expect(parseCondition("NOT record.a IN (1, 2)")).toMatchObject({ kind: "not", operand: { kind: "in" } });
    expect(parseCondition("NOT NOT record.a = 1")).toMatchObject({
      kind: "not",
      operand: { kind: "not", operand: { kind: "compare" } },
    });
    expect(parseCondition("NOT record.a = 1 AND record.b = 2")).toMatchObject({
      kind: "and",
      left: { kind: "not", operand: { kind: "compare" } },
    });
    expect(parseCondition("NOT record.a = 1 OR record.b = 2")).toMatchObject({
      kind: "or",
      left: { kind: "not", operand: { kind: "compare" } },
    });
    expect(parseCondition("record.b = 2 AND NOT record.a = 1")).toMatchObject({
      kind: "and",
      right: { kind: "not", operand: { kind: "compare" } },
    });
  });

  it("accepts a parenthesized NOT as a comparison operand", () => {
    expect(parseCondition("record.a = (NOT record.b)")).toMatchObject({
      kind: "compare",
      right: { kind: "not", operand: { kind: "record", field: "b" } },
    });
  });

  it("lets parentheses override precedence", () => {
    expect(parseCondition("(record.a = 1 OR record.b = 2) AND record.c = 3")).toMatchObject({
      kind: "and",
      left: { kind: "or" },
    });
  });

  it("treats keywords case-insensitively and field names case-sensitively", () => {
    expect(parseCondition("record.Amount > 1 and record.b < 2")).toMatchObject({
      kind: "and",
      left: { right: { value: 1 }, left: { field: "Amount" } },
    });
  });

  it.each([
    ["LIKE", "record.name LIKE 'a%'"],
    ["ILIKE", "record.name ILIKE 'a%'"],
    ["child_of", "record child_of record.parent_id"],
    ["parent_of", "record parent_of record.parent_id"],
  ])("rejects the search-only operator %s", (operator, src) => {
    const error = parseError(() => parseCondition(src));
    expect(error.phase).toBe("parse");
    expect(error.message).toContain(operator);
  });

  it.each([
    ["JavaScript equality", "record.a == 1"],
    ["strict equality", "record.a === 1"],
    ["strict inequality", "record.a !== 1"],
    ["logical and", "record.a = 1 && record.b = 2"],
    ["logical or", "record.a = 1 || record.b = 2"],
    ["negation", "!record.a"],
    ["unparenthesized NOT as a comparison operand", "record.a = NOT record.b"],
    ["unparenthesized NOT inside IN", "record.a IN (NOT record.b)"],
    ["method call", "user.roles.includes('a')"],
    ["unknown user attribute", "user.email = 'a'"],
    ["unknown function", "lower(record.a) = 'a'"],
    ["tenant binding", "tenant.country_code = 'GH'"],
    ["bare record", "record = 1"],
    ["arithmetic", "record.a + 1 > 2"],
    ["negative number", "record.a > -1"],
    ["unterminated string", "record.a = 'x"],
    ["dangling operator", "record.a ="],
    ["unclosed paren", "(record.a = 1"],
    ["trailing tokens", "record.a = 1 record.b"],
    ["empty expression", ""],
    ["non-literal role argument", "user_has_role(record.a)"],
    ["dynamic code", "constructor.constructor('return 1')()"],
  ])("rejects %s", (_name, src) => {
    expect(parseError(() => parseCondition(src)).phase).toBe("parse");
  });

  it("accepts a field named like an Object.prototype member", () => {
    expect(parseCondition("record.constructor IS NULL")).toMatchObject({
      kind: "isNull",
      operand: { kind: "record", field: "constructor" },
    });
  });

  describe("bounds on untrusted manifest input", () => {
    it.each([
      ["deeply nested parentheses", `${"(".repeat(70)}true${")".repeat(70)}`],
      ["a long chain of NOT", `${"NOT ".repeat(70)}true`],
      ["an overlong expression", `${"true AND ".repeat(400)}true`],
      ["a number literal too large to represent", `record.a > ${"9".repeat(400)}`],
      ["a missing expression", undefined],
      ["a non-string expression", 42],
    ])("rejects %s with a parse error rather than throwing", (_name, src) => {
      expect(parseError(() => parseCondition(src as string)).phase).toBe("parse");
    });

    it("accepts a moderately nested expression", () => {
      expect(() => parseCondition(`${"(".repeat(20)}true${")".repeat(20)}`)).not.toThrow();
    });
  });

  it("reports the position of the offending token", () => {
    const error = parseError(() => parseCondition("record.a = 1 AND"));
    expect(error.position).toBe(16);
    expect(error.expression).toBe("record.a = 1 AND");
  });
});

describe("parseValue", () => {
  it("parses arithmetic with standard precedence", () => {
    expect(parseValue("record.a + record.b * 2")).toEqual({
      kind: "arithmetic",
      op: "+",
      left: { kind: "record", field: "a" },
      right: {
        kind: "arithmetic",
        op: "*",
        left: { kind: "record", field: "b" },
        right: { kind: "number", value: 2 },
      },
    });
  });

  it("is left-associative", () => {
    expect(parseValue("10 - 4 - 3")).toMatchObject({
      op: "-",
      left: { kind: "arithmetic", op: "-" },
      right: { value: 3 },
    });
  });

  it("parses parentheses, unary minus and decimal literals", () => {
    expect(parseValue("-(record.a + 1.5)")).toMatchObject({
      kind: "negate",
      operand: { kind: "arithmetic", right: { value: 1.5 } },
    });
  });

  it("parses the two whitelisted functions with nested expressions", () => {
    expect(parseValue("PERCENT((record.a - record.b) / record.a, 1)")).toMatchObject({
      kind: "call",
      fn: "PERCENT",
      args: [
        { kind: "arithmetic", op: "/" },
        { kind: "number", value: 1 },
      ],
    });
    expect(parseValue("ROUND(record.a, 2)")).toMatchObject({ kind: "call", fn: "ROUND" });
  });

  it.each([
    ["unknown function", "FLOOR(record.a, 1)"],
    ["lowercase function", "round(record.a, 1)"],
    ["wrong arity", "ROUND(record.a)"],
    ["extra argument", "ROUND(record.a, 1, 2)"],
    ["boolean operator", "record.a > 1"],
    ["string literal", "'abc'"],
    ["user binding", "user.id"],
    ["method call", "record.a.toFixed(2)"],
    ["unbalanced paren", "(record.a + 1"],
    ["empty expression", ""],
    ["deeply nested parentheses", `${"(".repeat(70)}1${")".repeat(70)}`],
    ["deeply nested calls", `${"ROUND(".repeat(70)}1${", 1)".repeat(70)}`],
    ["a long chain of unary minus", `${"-".repeat(70)}1`],
    ["an overlong expression", `${"1 + ".repeat(600)}1`],
    ["a number literal too large to represent", "9".repeat(400)],
    ["a non-string expression", undefined],
  ])("rejects %s", (_name, src) => {
    expect(parseError(() => parseValue(src as string)).phase).toBe("parse");
  });
});
