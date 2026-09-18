import { describe, expect, it } from "vitest";
import { DomainExpressionError } from "./errors.js";
import { evaluateCondition, evaluateValue } from "./evaluate.js";
import type { ConditionBindings } from "./interpreter.js";

function bindings(record: Record<string, unknown>, user: Partial<ConditionBindings["user"]> = {}): ConditionBindings {
  return {
    record,
    user: {
      id: "u1",
      contactId: "c1",
      tenantId: "t1",
      roles: ["sales_rep"],
      permissions: new Set(["sales:order:read"]),
      ...user,
    },
  };
}

function condition(src: string, record: Record<string, unknown> = {}, user?: Partial<ConditionBindings["user"]>) {
  return evaluateCondition(src, bindings(record, user));
}

function truthy(src: string, record: Record<string, unknown> = {}) {
  const result = condition(src, record);
  if (!result.ok) throw result.error;
  return result.value;
}

function value(src: string, record: Record<string, unknown> = {}) {
  const result = evaluateValue(src, { record });
  if (!result.ok) throw result.error;
  return result.value;
}

function failure(result: { ok: boolean; error?: DomainExpressionError }): DomainExpressionError {
  if (result.ok || result.error === undefined) throw new Error("expected a failure");
  return result.error;
}

const circular: Record<string, unknown> = {};
circular.self = circular;

describe("evaluateCondition", () => {
  it("compares record fields with literals", () => {
    expect(truthy("record.state = 'draft'", { state: "draft" })).toBe(true);
    expect(truthy("record.state = 'draft'", { state: "done" })).toBe(false);
    expect(truthy("record.state != 'draft'", { state: "done" })).toBe(true);
    expect(truthy("record.amount_total > 1000", { amount_total: 1500 })).toBe(true);
    expect(truthy("record.amount_total <= 1000", { amount_total: 1000 })).toBe(true);
  });

  it("compares a decimal string field numerically", () => {
    expect(truthy("record.amount_total > 1000", { amount_total: "1500.00" })).toBe(true);
    expect(truthy("record.amount_total = 1500", { amount_total: "1500.00" })).toBe(true);
    expect(truthy("record.amount_total < 100", { amount_total: "1500.00" })).toBe(false);
  });

  it("compares two strings lexicographically", () => {
    expect(truthy("record.due < '2026-10-01'", { due: "2026-09-18" })).toBe(true);
  });

  it("binds user attributes from the session", () => {
    expect(condition("record.owner = current_user.contact_id", { owner: "c1" })).toEqual({ ok: true, value: true });
    expect(condition("record.owner = user.id", { owner: "u1" })).toEqual({ ok: true, value: true });
    expect(condition("user.tenant_id = 't1'")).toEqual({ ok: true, value: true });
  });

  it("checks roles and permissions", () => {
    expect(truthy("user_has_role('sales_rep')")).toBe(true);
    expect(truthy("user_has_role('sales_manager')")).toBe(false);
    expect(truthy("user_has_permission('sales:order:read')")).toBe(true);
    expect(truthy("user_has_permission('sales:order:write')")).toBe(false);
  });

  it("combines AND, OR and NOT", () => {
    const record = { type: "company", country_code: "GH" };
    expect(truthy("record.type = 'company' AND record.country_code = 'GH'", record)).toBe(true);
    expect(truthy("record.type = 'person' OR record.country_code = 'GH'", record)).toBe(true);
    expect(truthy("NOT user_has_role('sales_rep')", record)).toBe(false);
    expect(truthy("NOT (record.type = 'person')", record)).toBe(true);
    expect(truthy("NOT record.type = 'person'", record)).toBe(true);
    expect(truthy("NOT record.type = 'company'", record)).toBe(false);
    expect(truthy("NOT record.type IN ('person', 'company')", record)).toBe(false);
    expect(truthy("NOT record.type = 'person' AND record.country_code = 'GH'", record)).toBe(true);
    expect(truthy("record.type = 'person' OR record.type = 'company' AND record.country_code = 'US'", record)).toBe(
      false,
    );
  });

  it("evaluates IN", () => {
    expect(truthy("record.type IN ('note', 'call')", { type: "call" })).toBe(true);
    expect(truthy("record.type IN ('note', 'call')", { type: "email" })).toBe(false);
    expect(truthy("record.n IN (1, 2, 3)", { n: "2" })).toBe(true);
  });

  it("evaluates IS NULL and IS NOT NULL, treating an absent field as null", () => {
    expect(truthy("record.user_id IS NULL", {})).toBe(true);
    expect(truthy("record.user_id IS NULL", { user_id: null })).toBe(true);
    expect(truthy("record.user_id IS NOT NULL", { user_id: "u1" })).toBe(true);
    expect(truthy("record.user_id IS NULL", { user_id: "u1" })).toBe(false);
    expect(truthy("NOT record.deleted_at IS NULL", { deleted_at: null })).toBe(false);
    expect(truthy("NOT record.deleted_at IS NULL", { deleted_at: "2026-09-18" })).toBe(true);
    expect(truthy("record.customer IS NOT NULL", { customer: { id: "c1" } })).toBe(true);
  });

  it("evaluates a boolean field directly", () => {
    expect(truthy("record.is_active", { is_active: true })).toBe(true);
    expect(truthy("record.is_active = false", { is_active: false })).toBe(true);
  });

  it("orders two decimal strings numerically but keeps = as a string comparison", () => {
    expect(truthy("record.amount < record.limit", { amount: "9", limit: "10" })).toBe(true);
    expect(truthy("record.amount >= record.limit", { amount: "1500.00", limit: "999.99" })).toBe(true);
    expect(truthy("record.code = '007'", { code: "7" })).toBe(false);
    expect(truthy("record.code = '007'", { code: "007" })).toBe(true);
    expect(truthy("record.name < 'b'", { name: "a" })).toBe(true);
  });

  it("does not resolve inherited Object.prototype members as record fields", () => {
    expect(truthy("record.toString IS NULL", {})).toBe(true);
    expect(truthy("record.constructor IS NULL", {})).toBe(true);
    expect(truthy("record.constructor = 'x'", { constructor: "x" })).toBe(true);
  });

  it("treats an undefined user attribute as null", () => {
    const result = evaluateCondition("user.contact_id IS NULL", {
      record: {},
      user: { id: "u1", contactId: undefined as unknown as null, tenantId: "t1", roles: [], permissions: new Set() },
    });
    expect(result).toEqual({ ok: true, value: true });
  });

  it("returns a failure for very large input instead of overflowing the stack", () => {
    expect(failure(condition(`${"(".repeat(20000)}true${")".repeat(20000)}`)).phase).toBe("parse");
    expect(failure(condition(`${"NOT ".repeat(20000)}true`)).phase).toBe("parse");
    expect(failure(condition("true AND ".repeat(20000))).phase).toBe("parse");
    expect(failure(condition(undefined as unknown as string)).phase).toBe("parse");
  });

  it("returns a failure for an oversized expression without caching it", () => {
    const src = `${"true AND ".repeat(400)}true`;
    expect(failure(condition(src)).phase).toBe("parse");
    expect(failure(condition(src))).not.toBe(failure(condition(src)));
  });

  it("accepts form feed and vertical tab as whitespace, like the Go lexer", () => {
    expect(truthy("record.a\f=\v1", { a: 1 })).toBe(true);
  });

  it("keeps evaluating correctly after the parse cache evicts entries", () => {
    for (let i = 0; i < 1500; i++) expect(truthy(`record.n = ${i}`, { n: i })).toBe(true);
    expect(truthy("record.n = 0", { n: 0 })).toBe(true);
  });

  describe("null handling matches SQL three-valued logic", () => {
    it("makes a comparison with null unknown, and unknown evaluates to false", () => {
      expect(truthy("record.state = 'draft'", { state: null })).toBe(false);
      expect(truthy("record.state != 'draft'", { state: null })).toBe(false);
      expect(truthy("record.state = null", { state: null })).toBe(false);
    });

    it("keeps unknown through NOT", () => {
      expect(truthy("NOT record.state = 'draft'", { state: null })).toBe(false);
      expect(truthy("NOT (record.state = 'draft')", { state: null })).toBe(false);
    });

    it("lets a decisive operand override unknown in AND and OR", () => {
      expect(truthy("record.state = 'draft' OR user_has_role('sales_rep')", { state: null })).toBe(true);
      expect(truthy("record.state = 'draft' AND user_has_role('nobody')", { state: null })).toBe(false);
      expect(truthy("record.state = 'draft' AND user_has_role('sales_rep')", { state: null })).toBe(false);
      expect(truthy("record.state = 'draft' OR user_has_role('nobody')", { state: null })).toBe(false);
    });

    it("makes IN unknown on a null operand and when no match is found beside a null item", () => {
      expect(truthy("record.type IN ('a')", { type: null })).toBe(false);
      expect(truthy("NOT record.type IN ('a')", { type: null })).toBe(false);
      expect(truthy("record.type IN ('a', null)", { type: "b" })).toBe(false);
      expect(truthy("NOT (record.type IN ('a', null))", { type: "b" })).toBe(false);
      expect(truthy("record.type IN ('a', null)", { type: "a" })).toBe(true);
    });

    it("treats a missing contact_id as null", () => {
      expect(condition("record.owner = user.contact_id", { owner: "c1" }, { contactId: null })).toEqual({
        ok: true,
        value: false,
      });
    });
  });

  describe("failures are distinct from false", () => {
    it("returns a parse failure for a malformed expression", () => {
      const error = failure(condition("record.state == 'draft'"));
      expect(error).toBeInstanceOf(DomainExpressionError);
      expect(error.phase).toBe("parse");
    });

    it("returns the same failure on every call", () => {
      expect(failure(condition("record.state =="))).toBe(failure(condition("record.state ==")));
    });

    it.each([
      ["a string against a boolean", "record.flag = 'yes'", { flag: true }],
      ["non-numeric text against a number", "record.code > 5", { code: "ABC" }],
      ["an object operand", "record.customer = 'c1'", { customer: { id: "c1" } }],
      ["ordering booleans", "record.flag > false", { flag: true }],
      ["NOT applied to a non-boolean value", "NOT record.name", { name: "acme" }],
      ["a non-boolean top-level result", "record.name", { name: "acme" }],
      ["a non-boolean operand of AND", "record.name AND record.flag", { name: "acme", flag: true }],
    ])("returns an evaluation failure for %s", (_name, src, record) => {
      const error = failure(condition(src, record));
      expect(error.phase).toBe("evaluate");
      expect(error.expression).toBe(src);
    });
  });
});

describe("evaluateValue", () => {
  it("evaluates arithmetic with standard precedence", () => {
    expect(value("record.a + record.b * 2", { a: 1, b: 3 })).toBe(7);
    expect(value("(record.a + record.b) * 2", { a: 1, b: 3 })).toBe(8);
    expect(value("record.a - record.b - 1", { a: 10, b: 4 })).toBe(5);
    expect(value("-record.a + 5", { a: 2 })).toBe(3);
    expect(value("record.a / 4", { a: 10 })).toBe(2.5);
  });

  it("reads decimal string fields as numbers", () => {
    expect(value("record.price * record.qty", { price: "12.50", qty: 4 })).toBe(50);
  });

  it("rounds half away from zero on the decimal representation", () => {
    expect(value("ROUND(record.a, 2)", { a: 1.005 })).toBe(1.01);
    expect(value("ROUND(record.a, 0)", { a: 2.5 })).toBe(3);
    expect(value("ROUND(record.a, 0)", { a: -2.5 })).toBe(-3);
    expect(value("ROUND(record.a, 1)", { a: 0.04 })).toBe(0);
    expect(Object.is(value("ROUND(record.a, 1)", { a: -0.04 }), 0)).toBe(true);
    expect(value("ROUND(record.a / record.b, 2)", { a: 10, b: 3 })).toBe(3.33);
  });

  it("formats a fraction as a percentage string", () => {
    expect(value("PERCENT(record.a, 1)", { a: 0.123 })).toBe("12.3%");
    expect(value("PERCENT(record.a, 0)", { a: 0.5 })).toBe("50%");
    expect(value("PERCENT(record.a, 1)", { a: 0.5 })).toBe("50.0%");
    expect(value("PERCENT(record.a, 1)", { a: 0.1235 })).toBe("12.4%");
    expect(value("PERCENT(record.a, 1)", { a: -0.0004 })).toBe("0.0%");
  });

  it("evaluates the margin example", () => {
    expect(
      value("PERCENT((record.amount_total - record.cost_total) / record.amount_total, 1)", {
        amount_total: "1000.00",
        cost_total: "750.00",
      }),
    ).toBe("25.0%");
  });

  describe("fails loudly", () => {
    function valueFailure(src: string, record: Record<string, unknown> = {}) {
      const result = evaluateValue(src, { record });
      return failure(result);
    }

    it.each([
      ["a null operand", "record.a + 1", { a: null }],
      ["an absent operand", "record.a + 1", {}],
      ["an empty-string operand", "record.a + 1", { a: "" }],
      ["a non-numeric operand", "record.a + 1", { a: "abc" }],
      ["an object operand", "record.a + 1", { a: {} }],
      ["division by zero", "record.a / record.b", { a: 1, b: 0 }],
      ["division by a zero expression", "1 / (record.a - record.a)", { a: 5 }],
      ["a null operand inside PERCENT", "PERCENT(record.a / record.b, 1)", { a: 1, b: null }],
      ["a fractional digits argument", "ROUND(record.a, 1.5)", { a: 1 }],
      ["a negative digits argument", "ROUND(record.a, -1)", { a: 1 }],
      ["an oversized digits argument", "ROUND(record.a, 99)", { a: 1 }],
      ["PERCENT used in arithmetic", "PERCENT(record.a, 1) + 1", { a: 1 }],
      ["an overflowing result", "record.a * record.a", { a: 1e200 }],
      ["a PERCENT too large to format", "PERCENT(record.a, 1)", { a: 1e20 }],
      ["an inherited property name", "record.toString + 1", {}],
      ["a bigint operand", "record.a + 1", { a: 10n }],
      ["a circular operand", "record.a + 1", { a: circular }],
      ["a ROUND that overflows", "ROUND(record.a, 15)", { a: 1e300 }],
    ])("on %s", (_name, src, record) => {
      const error = valueFailure(src, record);
      expect(error.phase).toBe("evaluate");
      expect(error.expression).toBe(src);
    });

    it("on a malformed expression, as a parse failure", () => {
      expect(valueFailure("record.a +").phase).toBe("parse");
    });
  });
});
