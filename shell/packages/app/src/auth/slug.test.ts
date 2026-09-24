import { describe, expect, it } from "vitest";
import { deriveSlug, isValidSlug } from "./slug.js";

// The same cases as the engine's TestDeriveSlug, so the two stay in step.
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
    ["abc-".repeat(30), "abc-".repeat(16).replace(/-+$/, "")],
  ])("derives %j as %j", (name, want) => {
    expect(deriveSlug(name)).toBe(want);
  });
});

describe("isValidSlug", () => {
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
