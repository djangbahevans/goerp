import type { ComparisonOp, Expr, UserAttribute, ValueExpr, ValueFunction } from "./ast.js";
import { DomainExpressionError } from "./errors.js";
import { type Token, type TokenKind, tokenize } from "./lexer.js";

const COMPARISON_OPS: Partial<Record<TokenKind, ComparisonOp>> = {
  eq: "=",
  neq: "!=",
  lt: "<",
  gt: ">",
  lte: "<=",
  gte: ">=",
};

const SEARCH_ONLY_OPS: Partial<Record<TokenKind, string>> = {
  like: "LIKE",
  ilike: "ILIKE",
  child_of: "child_of",
  parent_of: "parent_of",
};

const USER_ATTRIBUTES: readonly string[] = ["id", "contact_id", "tenant_id"] satisfies UserAttribute[];

export const MAX_EXPRESSION_LENGTH = 2048;
const MAX_NESTING_DEPTH = 64;

const VALUE_FUNCTIONS: readonly string[] = ["ROUND", "PERCENT"] satisfies ValueFunction[];

class Cursor {
  private index = 0;
  private depth = 0;
  private readonly tokens: Token[];
  readonly src: string;

  constructor(src: string, tokens: Token[]) {
    this.src = src;
    this.tokens = tokens;
  }

  get current(): Token {
    return this.tokens[this.index] as Token;
  }

  advance(): Token {
    const token = this.current;
    if (this.index < this.tokens.length - 1) this.index++;
    return token;
  }

  accept(kind: TokenKind): boolean {
    if (this.current.kind !== kind) return false;
    this.advance();
    return true;
  }

  expect(kind: TokenKind, what: string): Token {
    if (this.current.kind !== kind) this.fail(`expected ${what}, got ${describe(this.current)}`, this.current.pos);
    return this.advance();
  }

  expectEnd(): void {
    if (this.current.kind !== "eof") this.fail(`unexpected ${describe(this.current)}`, this.current.pos);
  }

  enter(): void {
    this.depth++;
    if (this.depth > MAX_NESTING_DEPTH) this.fail("expression is nested too deeply", this.current.pos);
  }

  leave(): void {
    this.depth--;
  }

  fail(message: string, position: number): never {
    throw new DomainExpressionError("parse", message, this.src, position);
  }
}

function describe(token: Token): string {
  return token.kind === "eof" ? "end of expression" : `"${token.text}"`;
}

function checkSource(src: string): void {
  if (typeof src !== "string") throw new DomainExpressionError("parse", "expression must be a string", String(src));
  if (src.length > MAX_EXPRESSION_LENGTH) {
    throw new DomainExpressionError("parse", `expression is longer than ${MAX_EXPRESSION_LENGTH} characters`, src);
  }
}

function numberLiteral(cur: Cursor, token: Token): number {
  const value = Number(token.text);
  if (!Number.isFinite(value)) cur.fail(`number literal "${token.text}" is too large`, token.pos);
  return value;
}

export function parseCondition(src: string): Expr {
  checkSource(src);
  const tokens = tokenize(src, { arithmetic: false });
  const cursor = new Cursor(src, tokens);
  for (const token of tokens) {
    const op = SEARCH_ONLY_OPS[token.kind];
    if (op !== undefined) cursor.fail(`${op} is search-domain and ABAC only, not supported here`, token.pos);
  }
  const expr = parseOr(cursor);
  cursor.expectEnd();
  return expr;
}

function parseOr(cur: Cursor): Expr {
  cur.enter();
  let left = parseAnd(cur);
  while (cur.accept("or")) left = { kind: "or", left, right: parseAnd(cur) };
  cur.leave();
  return left;
}

function parseAnd(cur: Cursor): Expr {
  let left = parseNot(cur);
  while (cur.accept("and")) left = { kind: "and", left, right: parseNot(cur) };
  return left;
}

// As in SQL, NOT binds below comparison, IS NULL and IN, and above AND.
function parseNot(cur: Cursor): Expr {
  if (!cur.accept("not")) return parseIn(cur);
  cur.enter();
  const operand = parseNot(cur);
  cur.leave();
  return { kind: "not", operand };
}

function parseIn(cur: Cursor): Expr {
  let left = parseIsNull(cur);
  while (cur.accept("in")) {
    cur.expect("lparen", `"("`);
    const values = [parseIsNull(cur)];
    while (cur.accept("comma")) values.push(parseIsNull(cur));
    cur.expect("rparen", `")"`);
    left = { kind: "in", operand: left, values };
  }
  return left;
}

function parseIsNull(cur: Cursor): Expr {
  let left = parseComparison(cur);
  while (cur.accept("is")) {
    const negated = cur.accept("not");
    cur.expect("null", `"NULL"`);
    left = { kind: "isNull", operand: left, negated };
  }
  return left;
}

function parseComparison(cur: Cursor): Expr {
  let left = parsePrimary(cur);
  for (;;) {
    const op = COMPARISON_OPS[cur.current.kind];
    if (op === undefined) return left;
    cur.advance();
    left = { kind: "compare", op, left, right: parsePrimary(cur) };
  }
}

function parsePrimary(cur: Cursor): Expr {
  const token = cur.current;
  switch (token.kind) {
    case "lparen": {
      cur.advance();
      const inner = parseOr(cur);
      cur.expect("rparen", `")"`);
      return inner;
    }
    case "true":
    case "false":
      cur.advance();
      return { kind: "literal", value: token.kind === "true" };
    case "null":
      cur.advance();
      return { kind: "literal", value: null };
    case "string":
      cur.advance();
      return { kind: "literal", value: token.text };
    case "number":
      cur.advance();
      return { kind: "literal", value: numberLiteral(cur, token) };
    case "ident":
      return parseIdentifier(cur);
    default:
      return cur.fail(`unexpected ${describe(token)}`, token.pos);
  }
}

function parseIdentifier(cur: Cursor): Expr {
  const token = cur.advance();
  switch (token.text) {
    case "record":
      return { kind: "record", field: parseRecordField(cur) };
    case "current_user":
    case "user": {
      cur.expect("dot", `"."`);
      const attr = cur.expect("ident", "user attribute");
      if (!USER_ATTRIBUTES.includes(attr.text)) {
        cur.fail(`unknown user attribute "${attr.text}" (expected id, contact_id or tenant_id)`, attr.pos);
      }
      return { kind: "user", attr: attr.text as UserAttribute };
    }
    case "tenant":
      return cur.fail("tenant.{field} is not bound in shell-evaluated expressions", token.pos);
    case "user_has_role":
      return { kind: "role", role: parseStringCall(cur) };
    case "user_has_permission":
      return { kind: "permission", permission: parseStringCall(cur) };
    default:
      return cur.fail(`unknown identifier "${token.text}"`, token.pos);
  }
}

function parseRecordField(cur: Cursor): string {
  if (cur.current.kind !== "dot") cur.fail(`expected "record.{field}"`, cur.current.pos);
  cur.advance();
  return cur.expect("ident", "field name").text;
}

function parseStringCall(cur: Cursor): string {
  cur.expect("lparen", `"("`);
  const arg = cur.expect("string", "string literal argument");
  cur.expect("rparen", `")"`);
  return arg.text;
}

export function parseValue(src: string): ValueExpr {
  checkSource(src);
  const cursor = new Cursor(src, tokenize(src, { arithmetic: true }));
  const expr = parseSum(cursor);
  cursor.expectEnd();
  return expr;
}

function parseSum(cur: Cursor): ValueExpr {
  cur.enter();
  let left = parseProduct(cur);
  for (;;) {
    const op = cur.current.kind === "plus" ? "+" : cur.current.kind === "minus" ? "-" : undefined;
    if (op === undefined) break;
    cur.advance();
    left = { kind: "arithmetic", op, left, right: parseProduct(cur) };
  }
  cur.leave();
  return left;
}

function parseProduct(cur: Cursor): ValueExpr {
  let left = parseNegation(cur);
  for (;;) {
    const op = cur.current.kind === "star" ? "*" : cur.current.kind === "slash" ? "/" : undefined;
    if (op === undefined) return left;
    cur.advance();
    left = { kind: "arithmetic", op, left, right: parseNegation(cur) };
  }
}

function parseNegation(cur: Cursor): ValueExpr {
  if (!cur.accept("minus")) return parseValuePrimary(cur);
  cur.enter();
  const operand = parseNegation(cur);
  cur.leave();
  return { kind: "negate", operand };
}

function parseValuePrimary(cur: Cursor): ValueExpr {
  const token = cur.current;
  switch (token.kind) {
    case "number":
      cur.advance();
      return { kind: "number", value: numberLiteral(cur, token) };
    case "lparen": {
      cur.advance();
      const inner = parseSum(cur);
      cur.expect("rparen", `")"`);
      return inner;
    }
    case "ident":
      return parseValueIdentifier(cur);
    default:
      return cur.fail(`unexpected ${describe(token)}`, token.pos);
  }
}

function parseValueIdentifier(cur: Cursor): ValueExpr {
  const token = cur.advance();
  if (token.text === "record") return { kind: "record", field: parseRecordField(cur) };
  if (!VALUE_FUNCTIONS.includes(token.text)) {
    const hint = VALUE_FUNCTIONS.includes(token.text.toUpperCase()) ? ` (function names are uppercase)` : "";
    cur.fail(`unknown identifier "${token.text}"${hint}`, token.pos);
  }

  cur.expect("lparen", `"("`);
  const args = [parseSum(cur)];
  while (cur.accept("comma")) args.push(parseSum(cur));
  cur.expect("rparen", `")"`);
  if (args.length !== 2) cur.fail(`${token.text} takes exactly 2 arguments, got ${args.length}`, token.pos);
  return { kind: "call", fn: token.text as ValueFunction, args };
}
