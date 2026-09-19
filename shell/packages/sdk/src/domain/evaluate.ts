import type { Expr, ValueExpr } from "./ast.js";
import { DomainExpressionError } from "./errors.js";
import { type ConditionBindings, interpretCondition, interpretValue, type ValueBindings } from "./interpreter.js";
import { MAX_EXPRESSION_LENGTH, parseCondition, parseValue } from "./parser.js";

export type DomainResult<T> = { ok: true; value: T } | { ok: false; error: DomainExpressionError };

// Expressions are re-evaluated on every keystroke; parse results are memoized.
const MAX_CACHED_EXPRESSIONS = 1000;
const conditionCache = new Map<string, Expr | DomainExpressionError>();
const valueCache = new Map<string, ValueExpr | DomainExpressionError>();

function parseCached<T extends object>(
  cache: Map<string, T | DomainExpressionError>,
  src: string,
  parse: (src: string) => T,
): T | DomainExpressionError {
  const cacheable = typeof src === "string" && src.length <= MAX_EXPRESSION_LENGTH;
  const cached = cacheable ? cache.get(src) : undefined;
  if (cached !== undefined) return cached;
  let entry: T | DomainExpressionError;
  try {
    entry = parse(src);
  } catch (error) {
    if (!(error instanceof DomainExpressionError)) throw error;
    entry = error;
  }
  if (!cacheable) return entry;
  if (cache.size >= MAX_CACHED_EXPRESSIONS) cache.delete(cache.keys().next().value as string);
  cache.set(src, entry);
  return entry;
}

function run<T>(compute: () => T): DomainResult<T> {
  try {
    return { ok: true, value: compute() };
  } catch (error) {
    if (!(error instanceof DomainExpressionError)) throw error;
    return { ok: false, error };
  }
}

// A malformed or unevaluable expression is `ok: false`, never `false`; an unknown result is `false`.
export function evaluateCondition(src: string, bindings: ConditionBindings): DomainResult<boolean> {
  const parsed = parseCached(conditionCache, src, parseCondition);
  if (parsed instanceof DomainExpressionError) return { ok: false, error: parsed };
  return run(() => interpretCondition(parsed, bindings, src));
}

// A top-level `PERCENT(...)` yields a string; everything else a number.
export function evaluateValue(src: string, bindings: ValueBindings): DomainResult<number | string> {
  const parsed = parseCached(valueCache, src, parseValue);
  if (parsed instanceof DomainExpressionError) return { ok: false, error: parsed };
  return run(() => interpretValue(parsed, bindings, src));
}
