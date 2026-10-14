import { RFC_EMAIL_REGEX, UUID_REGEX } from "valibot";

export type Validator<Value = unknown> = (value: Value) => string | undefined;

function isEmpty(value: unknown): boolean {
  return value == null || (typeof value === "string" && value.trim() === "");
}

function required(value: unknown): string | undefined {
  return isEmpty(value) || (Array.isArray(value) && value.length === 0) ? "Required" : undefined;
}

function email(value: unknown): string | undefined {
  if (isEmpty(value)) return undefined;
  return typeof value === "string" && value.trim() === value && RFC_EMAIL_REGEX.test(value)
    ? undefined
    : "Invalid email";
}

function url(value: unknown): string | undefined {
  if (isEmpty(value)) return undefined;
  if (typeof value !== "string" || value.trim() !== value) return "Invalid URL";

  try {
    new URL(value);
    return undefined;
  } catch {
    return "Invalid URL";
  }
}

function lengthLimit(limit: number): void {
  if (!Number.isSafeInteger(limit) || limit < 0) {
    throw new RangeError("Length limit must be a non-negative safe integer");
  }
}

function minLength(limit: number): Validator {
  lengthLimit(limit);
  return (value) => {
    if (isEmpty(value)) return undefined;
    return typeof value === "string" && value.length >= limit
      ? undefined
      : `Must be at least ${limit} ${limit === 1 ? "character" : "characters"}`;
  };
}

function maxLength(limit: number): Validator {
  lengthLimit(limit);
  return (value) => {
    if (isEmpty(value)) return undefined;
    return typeof value === "string" && value.length <= limit
      ? undefined
      : `Must be at most ${limit} ${limit === 1 ? "character" : "characters"}`;
  };
}

function numberLimit(limit: number): void {
  if (!Number.isFinite(limit)) throw new RangeError("Numeric limit must be finite");
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function min(limit: number): Validator {
  numberLimit(limit);
  return (value) => {
    if (isEmpty(value)) return undefined;
    return isFiniteNumber(value) && value >= limit ? undefined : `Must be at least ${limit}`;
  };
}

function max(limit: number): Validator {
  numberLimit(limit);
  return (value) => {
    if (isEmpty(value)) return undefined;
    return isFiniteNumber(value) && value <= limit ? undefined : `Must be at most ${limit}`;
  };
}

function positive(value: unknown): string | undefined {
  if (isEmpty(value)) return undefined;
  return isFiniteNumber(value) && value > 0 ? undefined : "Must be positive";
}

function uuid(value: unknown): string | undefined {
  if (isEmpty(value)) return undefined;
  return typeof value === "string" && value.trim() === value && UUID_REGEX.test(value) ? undefined : "Invalid UUID";
}

function compose<Value = unknown>(...rules: readonly Validator<Value>[]): Validator<Value> {
  return (value) => {
    for (const rule of rules) {
      const error = rule(value);
      if (error !== undefined) return error;
    }
    return undefined;
  };
}

export const validators = { required, email, url, minLength, maxLength, min, max, positive, uuid, compose };
