import { describe, expect, it, vi } from "vitest";
import { fuzzyScore, searchCommands } from "./command-search.js";
import type { Command } from "./command-types.js";

describe("fuzzyScore", () => {
  it("matches a subsequence regardless of case", () => {
    expect(fuzzyScore("nc", "New Contact")).not.toBeNull();
    expect(fuzzyScore("NC", "new contact")).not.toBeNull();
  });

  it("returns null when a query character is missing entirely", () => {
    expect(fuzzyScore("xyz", "New Contact")).toBeNull();
  });

  it("returns null when the query characters appear out of order", () => {
    expect(fuzzyScore("cb", "abc")).toBeNull();
  });

  it("scores a contiguous match higher than the same letters scattered", () => {
    const contiguous = fuzzyScore("con", "Contact") as number;
    const scattered = fuzzyScore("cot", "Contact") as number;
    expect(contiguous).toBeGreaterThan(scattered);
  });

  it("scores an earlier match higher than the same subsequence found later", () => {
    const early = fuzzyScore("new", "New Contact") as number;
    const late = fuzzyScore("new", "Add a brand New Contact") as number;
    expect(early).toBeGreaterThan(late);
  });

  it("treats an empty query as a zero-score match", () => {
    expect(fuzzyScore("", "anything")).toBe(0);
  });
});

function command(overrides: Partial<Command> = {}): Command {
  return { id: "c1", label: "New Contact", action: vi.fn(), ...overrides };
}

describe("searchCommands", () => {
  it("returns every command unfiltered for an empty query", () => {
    const commands = [command({ id: "1" }), command({ id: "2" })];
    expect(searchCommands(commands, "")).toEqual(commands);
  });

  it("filters out commands with no matching field", () => {
    const commands = [command({ id: "1", label: "New Contact" }), command({ id: "2", label: "Sign Out" })];
    expect(searchCommands(commands, "contact").map((c) => c.id)).toEqual(["1"]);
  });

  it("matches against description and keywords, not just label", () => {
    const byDescription = command({ id: "1", label: "Foo", description: "archives the record" });
    const byKeyword = command({ id: "2", label: "Bar", keywords: ["archive"] });
    const noMatch = command({ id: "3", label: "Baz" });
    expect(
      searchCommands([byDescription, byKeyword, noMatch], "archive")
        .map((c) => c.id)
        .sort(),
    ).toEqual(["1", "2"]);
  });

  it("ranks the best-scoring match first", () => {
    const weak = command({ id: "weak", label: "Add a brand New Contact" });
    const strong = command({ id: "strong", label: "New Contact" });
    expect(searchCommands([weak, strong], "new").map((c) => c.id)).toEqual(["strong", "weak"]);
  });

  it("preserves original order between equally-scored commands", () => {
    const a = command({ id: "a", label: "Sign Out" });
    const b = command({ id: "b", label: "Sign In" });
    expect(searchCommands([a, b], "sign").map((c) => c.id)).toEqual(["a", "b"]);
  });
});
