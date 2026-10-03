import { describe, expect, it } from "vitest";
import { deriveSlug, isValidSlug } from "./slug.js";

describe("deriveSlug", () => {
  it.each([
    ["Acme Corp", "acme-corp"],
    ["  Acme   Corp  ", "acme-corp"],
    ["Café Ghana Ltd.", "cafe-ghana-ltd"],
    ["O'Brien & Sons", "o-brien-sons"],
    ["3M Ghana", "co-3m-ghana"],
    ["--Acme--", "acme"],
    ["安全", ""],
    ["AB", "ab"],
    ["a".repeat(56), "a".repeat(56)],
    ["a".repeat(57), "a".repeat(56)],
    [`3${"a".repeat(100)}`, `co-3${"a".repeat(52)}`],
    ["abc-".repeat(30), "abc-".repeat(14).replace(/-+$/, "")],
  ])("derives %j as %j", (name, want) => {
    expect(deriveSlug(name)).toBe(want);
  });
});

describe("isValidSlug", () => {
  it.each([2, 3, 55, 56, 57, 64])("validates a %i-character slug", (length) => {
    expect(isValidSlug("a".repeat(length))).toBe(length >= 3 && length <= 56);
  });

  it.each([
    ["Acme Corp", true],
    ["3M Ghana", true],
    ["AB", false],
    ["安全", false],
    ["abc-".repeat(30), true],
  ])("reports %j's slug valid: %j", (name, want) => {
    expect(isValidSlug(deriveSlug(name))).toBe(want);
  });
});
