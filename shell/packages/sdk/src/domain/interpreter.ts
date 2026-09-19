import type { ComparisonOp, Expr, ValueExpr } from "./ast.js";
import { DomainExpressionError } from "./errors.js";

export interface UserBindings {
  id: string;
  contactId: string | null;
  tenantId: string;
  roles: readonly string[];
  permissions: ReadonlySet<string>;
}

export interface ValueBindings {
  record: Readonly<Record<string, unknown>>;
}

export interface ConditionBindings extends ValueBindings {
  user: UserBindings;
}

// null is SQL's "unknown", so a condition matches its Postgres RLS compilation.
type Value = null | boolean | number | string | object;

function evaluationError(message: string, src: string): DomainExpressionError {
  return new DomainExpressionError("evaluate", message, src);
}

function describeValue(value: Value): string {
  return value === null ? "null" : typeof value === "object" ? "an object" : `${typeof value} ${JSON.stringify(value)}`;
}

function recordField(record: Readonly<Record<string, unknown>>, field: string): unknown {
  return Object.hasOwn(record, field) ? record[field] : undefined;
}

function scalar(raw: unknown, src: string, label: string): Value {
  if (raw === undefined || raw === null) return null;
  if (typeof raw === "boolean" || typeof raw === "number" || typeof raw === "string" || typeof raw === "object") {
    return raw;
  }
  throw evaluationError(`${label} holds an unsupported ${typeof raw} value`, src);
}

export function interpretCondition(expr: Expr, bindings: ConditionBindings, src: string): boolean {
  return evalBoolean(expr, bindings, src) === true;
}

function evalBoolean(expr: Expr, b: ConditionBindings, src: string): boolean | null {
  const value = evalExpr(expr, b, src);
  if (value === null || typeof value === "boolean") return value;
  throw evaluationError(`expected a boolean, got ${describeValue(value)}`, src);
}

function evalExpr(expr: Expr, b: ConditionBindings, src: string): Value {
  switch (expr.kind) {
    case "literal":
      return expr.value;
    case "record":
      return scalar(recordField(b.record, expr.field), src, `record.${expr.field}`);
    case "user":
      return (expr.attr === "id" ? b.user.id : expr.attr === "contact_id" ? b.user.contactId : b.user.tenantId) ?? null;
    case "role":
      return b.user.roles.includes(expr.role);
    case "permission":
      return b.user.permissions.has(expr.permission);
    case "not": {
      const operand = evalBoolean(expr.operand, b, src);
      return operand === null ? null : !operand;
    }
    case "and": {
      const left = evalBoolean(expr.left, b, src);
      if (left === false) return false;
      const right = evalBoolean(expr.right, b, src);
      if (right === false) return false;
      return left === null || right === null ? null : true;
    }
    case "or": {
      const left = evalBoolean(expr.left, b, src);
      if (left === true) return true;
      const right = evalBoolean(expr.right, b, src);
      if (right === true) return true;
      return left === null || right === null ? null : false;
    }
    case "isNull": {
      const isNull = evalExpr(expr.operand, b, src) === null;
      return expr.negated ? !isNull : isNull;
    }
    case "compare": {
      const left = evalExpr(expr.left, b, src);
      const right = evalExpr(expr.right, b, src);
      if (left === null || right === null) return null;
      return compare(expr.op, left, right, src);
    }
    case "in": {
      const operand = evalExpr(expr.operand, b, src);
      if (operand === null) return null;
      let sawNull = false;
      for (const item of expr.values) {
        const candidate = evalExpr(item, b, src);
        if (candidate === null) sawNull = true;
        else if (compare("=", operand, candidate, src)) return true;
      }
      return sawNull ? null : false;
    }
  }
}

const NUMERIC_STRING = /^-?\d+(\.\d+)?$/;

// Decimal fields arrive as strings: they compare numerically against numbers
// and order numerically against each other, but = / != on two strings stays
// textual so '007' does not equal '7'.
function toNumeric(value: number | string): number | undefined {
  if (typeof value === "number") return value;
  return NUMERIC_STRING.test(value) ? Number(value) : undefined;
}

function compare(op: ComparisonOp, left: Value, right: Value, src: string): boolean {
  if (typeof left === "object" || typeof right === "object") {
    throw evaluationError(`cannot compare ${describeValue(left)} with ${describeValue(right)}`, src);
  }
  if (typeof left === "boolean" && typeof right === "boolean") {
    if (op === "=") return left === right;
    if (op === "!=") return left !== right;
    throw evaluationError(`booleans only support = and !=, got ${op}`, src);
  }
  if (typeof left === "boolean" || typeof right === "boolean") {
    throw evaluationError(`cannot compare ${describeValue(left)} with ${describeValue(right)}`, src);
  }

  let a: number | string = left;
  let b: number | string = right;
  const ordering = op !== "=" && op !== "!=";
  if (typeof a !== typeof b || ordering) {
    const numericA = toNumeric(a);
    const numericB = toNumeric(b);
    if (numericA !== undefined && numericB !== undefined) {
      a = numericA;
      b = numericB;
    } else if (typeof a !== typeof b) {
      throw evaluationError(`cannot compare ${describeValue(left)} with ${describeValue(right)}`, src);
    }
  }

  switch (op) {
    case "=":
      return a === b;
    case "!=":
      return a !== b;
    case "<":
      return a < b;
    case ">":
      return a > b;
    case "<=":
      return a <= b;
    case ">=":
      return a >= b;
  }
}

export function interpretValue(expr: ValueExpr, bindings: ValueBindings, src: string): number | string {
  return evalValue(expr, bindings, src);
}

function evalNumber(expr: ValueExpr, b: ValueBindings, src: string): number {
  const value = evalValue(expr, b, src);
  if (typeof value !== "number") throw evaluationError(`${describeValue(value)} cannot be used in arithmetic`, src);
  return value;
}

function evalValue(expr: ValueExpr, b: ValueBindings, src: string): number | string {
  switch (expr.kind) {
    case "number":
      return expr.value;
    case "record":
      return recordNumber(b, expr.field, src);
    case "negate":
      return -evalNumber(expr.operand, b, src);
    case "arithmetic":
      return arithmetic(expr.op, evalNumber(expr.left, b, src), evalNumber(expr.right, b, src), src);
    case "call": {
      const [valueArg, digitsArg] = expr.args as [ValueExpr, ValueExpr];
      const value = evalNumber(valueArg, b, src);
      const digits = evalNumber(digitsArg, b, src);
      if (!Number.isInteger(digits) || digits < 0 || digits > MAX_DIGITS) {
        throw evaluationError(`${expr.fn} digits must be an integer from 0 to ${MAX_DIGITS}, got ${digits}`, src);
      }
      const rounded = roundHalfAwayFromZero(value, digits, expr.fn === "PERCENT" ? 2 : 0);
      if (!Number.isFinite(rounded)) throw evaluationError(`${expr.fn} result is not a finite number`, src);
      if (expr.fn === "ROUND") return rounded;
      if (Math.abs(rounded) >= MAX_PERCENT) throw evaluationError("PERCENT result is too large to format", src);
      return `${rounded.toFixed(digits)}%`;
    }
  }
}

const MAX_DIGITS = 15;
// Number#toFixed switches to exponent notation at 1e21.
const MAX_PERCENT = 1e21;

function recordNumber(b: ValueBindings, field: string, src: string): number {
  const raw = recordField(b.record, field);
  if (raw === undefined || raw === null || raw === "") throw evaluationError(`record.${field} is empty`, src);
  const numeric = typeof raw === "number" || typeof raw === "string" ? toNumeric(raw) : undefined;
  if (numeric === undefined || !Number.isFinite(numeric)) {
    const shown = typeof raw === "string" ? `: ${JSON.stringify(raw)}` : "";
    throw evaluationError(`record.${field} is not a number${shown}`, src);
  }
  return numeric;
}

function arithmetic(op: "+" | "-" | "*" | "/", left: number, right: number, src: string): number {
  if (op === "/" && right === 0) throw evaluationError("division by zero", src);
  const result = op === "+" ? left + right : op === "-" ? left - right : op === "*" ? left * right : left / right;
  if (!Number.isFinite(result)) throw evaluationError("result is not a finite number", src);
  return result;
}

// Shifts via the string form (1.005e2 is exactly 100.5); multiplying gives 100.49999999999999.
function shiftDecimal(value: number, places: number): number {
  const shifted = Number(`${value}e${places}`);
  return Number.isFinite(shifted) ? shifted : value * 10 ** places;
}

function roundHalfAwayFromZero(value: number, digits: number, extraShift = 0): number {
  const sign = value < 0 ? -1 : 1;
  const rounded = Math.round(shiftDecimal(Math.abs(value), digits + extraShift));
  const result = sign * shiftDecimal(rounded, -digits);
  return result === 0 ? 0 : result;
}
