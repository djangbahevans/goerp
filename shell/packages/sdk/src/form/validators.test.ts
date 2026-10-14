import { describe, expect, it, vi } from "vitest";
import { type Validator, validators } from "./index.js";

describe("required", () => {
  it.each([undefined, null, "", " \t\n", []])("rejects an empty value: %j", (value) => {
    expect(validators.required(value)).toBe("Required");
  });

  it.each([0, false, "0", " name ", [0], {}])("accepts a present value: %j", (value) => {
    expect(validators.required(value)).toBeUndefined();
  });
});

const optionalRules: [string, Validator][] = [
  ["email", validators.email],
  ["url", validators.url],
  ["minLength", validators.minLength(3)],
  ["maxLength", validators.maxLength(3)],
  ["min", validators.min(3)],
  ["max", validators.max(3)],
  ["positive", validators.positive],
  ["uuid", validators.uuid],
];

describe.each(optionalRules)("%s", (_name, rule) => {
  it.each([undefined, null, "", " \t\n"])("accepts an optional empty value: %j", (value) => {
    expect(rule(value)).toBeUndefined();
  });

  it.each([true, false, {}, [], ["value"]])("rejects an incompatible type: %j", (value) => {
    expect(rule(value)).toEqual(expect.any(String));
  });
});

describe("email", () => {
  it.each(["user@example.com", "first.last+tag@example.co.uk", "o'hara@example.com", "user@localhost"])(
    "accepts %s",
    (value) => {
      expect(validators.email(value)).toBeUndefined();
    },
  );

  it.each([
    "user",
    "user@",
    "@example.com",
    "a@@example.com",
    "a b@example.com",
    "a@-example.com",
    "a@example..com",
    " user@example.com",
    "user@example.com\n",
    1,
  ])("rejects %j", (value) => {
    expect(validators.email(value)).toBe("Invalid email");
  });
});

describe("url", () => {
  it.each([
    "https://example.com/path?q=1#section",
    "http://localhost:8080",
    "ftp://example.com/file",
    "mailto:user@example.com",
  ])("accepts an absolute URL: %s", (value) => {
    expect(validators.url(value)).toBeUndefined();
  });

  it.each([
    "example.com",
    "/relative",
    "//example.com",
    "http://",
    "https://exa mple.com",
    " https://example.com",
    "https://example.com\n",
    1,
  ])("rejects %j", (value) => {
    expect(validators.url(value)).toBe("Invalid URL");
  });
});

describe("length limits", () => {
  it.each([
    ["ab", "Must be at least 3 characters", undefined],
    ["abc", undefined, undefined],
    ["abcd", undefined, "Must be at most 3 characters"],
    [3, "Must be at least 3 characters", "Must be at most 3 characters"],
  ])("checks inclusive boundaries for %j", (value, minError, maxError) => {
    expect(validators.minLength(3)(value)).toBe(minError);
    expect(validators.maxLength(3)(value)).toBe(maxError);
  });

  it("uses UTF-16 string length and accepts zero limits", () => {
    expect(validators.minLength(2)("😀")).toBeUndefined();
    expect(validators.maxLength(1)("😀")).toBe("Must be at most 1 character");
    expect(validators.minLength(0)("a")).toBeUndefined();
    expect(validators.maxLength(0)("a")).toBe("Must be at most 0 characters");
  });

  it.each([-1, 0.5, NaN, Infinity, -Infinity, Number.MAX_SAFE_INTEGER + 1])(
    "rejects invalid factory limit %s",
    (limit) => {
      expect(() => validators.minLength(limit)).toThrow(RangeError);
      expect(() => validators.maxLength(limit)).toThrow(RangeError);
    },
  );
});

describe("numeric limits", () => {
  it.each([
    [1.4, "Must be at least 1.5", undefined],
    [1.5, undefined, undefined],
    [1.6, undefined, "Must be at most 1.5"],
    ["1.5", "Must be at least 1.5", "Must be at most 1.5"],
  ])("checks inclusive boundaries without coercion for %j", (value, minError, maxError) => {
    expect(validators.min(1.5)(value)).toBe(minError);
    expect(validators.max(1.5)(value)).toBe(maxError);
  });

  it("accepts negative and zero limits", () => {
    expect(validators.min(-2)(-2)).toBeUndefined();
    expect(validators.max(-2)(-2)).toBeUndefined();
    expect(validators.min(0)(0)).toBeUndefined();
    expect(validators.max(0)(0)).toBeUndefined();
  });

  it.each([NaN, Infinity, -Infinity])("rejects non-finite input and limits: %s", (value) => {
    expect(validators.min(1)(value)).toBe("Must be at least 1");
    expect(validators.max(1)(value)).toBe("Must be at most 1");
    expect(() => validators.min(value)).toThrow(RangeError);
    expect(() => validators.max(value)).toThrow(RangeError);
  });
});

describe("positive", () => {
  it.each([Number.MIN_VALUE, 0.5, 1, Number.MAX_VALUE])("accepts %s", (value) => {
    expect(validators.positive(value)).toBeUndefined();
  });

  it.each([0, -0, -0.5, -1, NaN, Infinity, -Infinity, "1"])("rejects %j", (value) => {
    expect(validators.positive(value)).toBe("Must be positive");
  });
});

describe("uuid", () => {
  it.each([
    "550e8400-e29b-41d4-a716-446655440000",
    "550E8400-E29B-41D4-A716-446655440000",
    "019a7ef0-1234-7abc-8def-0123456789ab",
    "00000000-0000-0000-0000-000000000000",
  ])("accepts %s", (value) => {
    expect(validators.uuid(value)).toBeUndefined();
  });

  it.each([
    "550e8400e29b41d4a716446655440000",
    "g50e8400-e29b-41d4-a716-446655440000",
    "550e8400-e29b-41d4-a716-44665544000",
    "{550e8400-e29b-41d4-a716-446655440000}",
    "550e8400-e29b-41d4-a716-446655440000\n",
    1,
  ])("rejects %j", (value) => {
    expect(validators.uuid(value)).toBe("Invalid UUID");
  });
});

describe("compose", () => {
  it("reports required before email", () => {
    const rule = validators.compose(validators.required, validators.email);
    expect(rule("")).toBe("Required");
    expect(rule("malformed")).toBe("Invalid email");
    expect(rule("user@example.com")).toBeUndefined();
  });

  it("runs in order and stops at the first defined error, including an empty message", () => {
    const first = vi.fn(() => undefined);
    const second = vi.fn(() => "");
    const third = vi.fn(() => "last error");
    expect(validators.compose(first, second, third)(42)).toBe("");
    expect(first).toHaveBeenCalledWith(42);
    expect(second).toHaveBeenCalledWith(42);
    expect(first.mock.invocationCallOrder[0]).toBeLessThan(second.mock.invocationCallOrder[0]!);
    expect(third).not.toHaveBeenCalled();
  });

  it("accepts no rules or all passing rules", () => {
    expect(validators.compose()(undefined)).toBeUndefined();
    expect(validators.compose(validators.min(0), validators.max(1))(0.5)).toBeUndefined();
  });
});
