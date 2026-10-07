import { describe, expect, it } from "vitest";
import type { ConfigEntry } from "./config-api.js";
import { draftOf, groupByCategory, parseDraft, sameDraft, splitList, widgetOf } from "./config-widgets.js";

function entry(overrides: Partial<ConfigEntry> = {}): ConfigEntry {
  return {
    key: "k",
    label: "K",
    description: "",
    type: "string",
    fieldType: "",
    category: "",
    required: false,
    options: [],
    min: null,
    max: null,
    encrypted: false,
    generated: false,
    restartRequired: false,
    default: null,
    isSet: false,
    value: null,
    ...overrides,
  };
}

const OPTIONS = [
  { value: "a", label: "A" },
  { value: "b", label: "B" },
];

describe("widgetOf", () => {
  it.each([
    ["an encrypted string", { encrypted: true }, "password"],
    ["a plain string", {}, "text"],
    ["a string asking for a textarea", { fieldType: "textarea" }, "textarea"],
    ["a boolean", { type: "boolean" }, "toggle"],
    ["an integer", { type: "integer" }, "number"],
    ["a float", { type: "float" }, "number"],
    ["a string with options", { options: OPTIONS }, "select"],
    ["an integer with options", { type: "integer", options: OPTIONS }, "select"],
    ["a string array with options", { type: "string[]", options: OPTIONS }, "multiselect"],
    ["a string array without options", { type: "string[]" }, "list"],
    ["a number array", { type: "integer[]" }, "list"],
    ["json", { type: "json" }, "json"],
    ["a duration", { type: "duration" }, "text"],
    ["a generated string", { generated: true }, "generated"],
    ["a generated encrypted string", { generated: true, encrypted: true }, "generated"],
    ["a generated boolean, whatever its type", { generated: true, type: "boolean" }, "generated"],
    ["a generated value with options", { generated: true, options: OPTIONS }, "generated"],
  ] as const)("renders %s as %s", (_, overrides, want) => {
    expect(widgetOf(entry(overrides))).toBe(want);
  });
});

describe("draftOf", () => {
  it("shows a set encrypted value as the mask and an unset one as empty", () => {
    expect(draftOf(entry({ encrypted: true, isSet: true, value: "***" }))).toBe("***");
    expect(draftOf(entry({ encrypted: true }))).toBe("");
  });

  it("starts each widget from its value", () => {
    expect(draftOf(entry({ type: "boolean", value: true }))).toBe(true);
    expect(draftOf(entry({ type: "boolean", value: null }))).toBe(false);
    expect(draftOf(entry({ type: "integer", value: 4 }))).toBe("4");
    expect(draftOf(entry({ value: "x" }))).toBe("x");
    expect(draftOf(entry({ type: "integer[]", value: [1, 2] }))).toEqual(["1", "2"]);
    expect(draftOf(entry({ type: "string[]", value: null }))).toEqual([]);
    expect(draftOf(entry({ type: "json", value: { a: 1 } }))).toBe('{\n  "a": 1\n}');
  });
});

describe("parseDraft", () => {
  it("parses numbers, rejecting text and, for integers, fractions", () => {
    expect(parseDraft(entry({ type: "integer" }), "4")).toEqual({ ok: true, value: 4 });
    expect(parseDraft(entry({ type: "integer" }), "4.5")).toMatchObject({ ok: false });
    expect(parseDraft(entry({ type: "integer" }), "abc")).toMatchObject({ ok: false });
    expect(parseDraft(entry({ type: "float" }), "4.5")).toEqual({ ok: true, value: 4.5 });
  });

  it("sends an integer or float select as a number", () => {
    expect(parseDraft(entry({ type: "integer", options: OPTIONS }), "3")).toEqual({ ok: true, value: 3 });
    expect(parseDraft(entry({ type: "float", options: OPTIONS }), "0.5")).toEqual({ ok: true, value: 0.5 });
    expect(parseDraft(entry({ type: "integer", options: OPTIONS }), "0.5")).toMatchObject({ ok: false });
  });

  it("parses array items by the array's type", () => {
    expect(parseDraft(entry({ type: "string[]" }), ["a", "b"])).toEqual({ ok: true, value: ["a", "b"] });
    expect(parseDraft(entry({ type: "integer[]" }), ["1", "2"])).toEqual({ ok: true, value: [1, 2] });
    expect(parseDraft(entry({ type: "integer[]" }), ["1", "x"])).toMatchObject({ ok: false });
    expect(parseDraft(entry({ type: "integer[]" }), ["1.5"])).toMatchObject({ ok: false });
  });

  it("parses json", () => {
    expect(parseDraft(entry({ type: "json" }), '{"a":1}')).toEqual({ ok: true, value: { a: 1 } });
    expect(parseDraft(entry({ type: "json" }), "{")).toMatchObject({ ok: false });
  });

  it("resets an emptied optional field and rejects an emptied required one", () => {
    expect(parseDraft(entry(), "")).toEqual({ ok: true, value: null });
    expect(parseDraft(entry({ type: "string[]" }), [])).toEqual({ ok: true, value: null });
    expect(parseDraft(entry({ required: true }), "")).toMatchObject({ ok: false });
    expect(parseDraft(entry({ required: true, default: "x" }), "")).toEqual({ ok: true, value: null });
  });

  it("keeps a toggle's false as a value", () => {
    expect(parseDraft(entry({ type: "boolean" }), false)).toEqual({ ok: true, value: false });
  });
});

describe("helpers", () => {
  it("compares drafts by value", () => {
    expect(sameDraft(["a"], ["a"])).toBe(true);
    expect(sameDraft(["a"], ["a", "b"])).toBe(false);
    expect(sameDraft("x", "x")).toBe(true);
    expect(sameDraft(true, false)).toBe(false);
  });

  it("splits a comma-separated list", () => {
    expect(splitList(" a, b ,, c ")).toEqual(["a", "b", "c"]);
    expect(splitList("")).toEqual([]);
  });

  it("groups entries by category in first-appearance order, uncategorised ones under Settings", () => {
    const groups = groupByCategory([
      entry({ key: "a", category: "Options" }),
      entry({ key: "b" }),
      entry({ key: "c", category: "Options" }),
      entry({ key: "d", category: "API" }),
    ]);
    expect(groups.map((g) => [g.category, g.entries.map((e) => e.key)])).toEqual([
      ["Options", ["a", "c"]],
      ["Settings", ["b"]],
      ["API", ["d"]],
    ]);
  });
});
