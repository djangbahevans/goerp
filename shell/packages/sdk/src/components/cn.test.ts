/// <reference types="vite/client" />
import { describe, expect, it } from "vitest";
import tokensCss from "../../../app/src/design/tokens.css?raw";
import { cn, RADIUS_TOKENS, SHADOW_TOKENS, Z_TOKENS } from "./cn.js";

function tokenNames(prefix: string): string[] {
  const names = new Set<string>();
  for (const match of tokensCss.matchAll(new RegExp(`--${prefix}-([a-z0-9-]+)\\s*:`, "g"))) {
    if (match[1]) names.add(match[1]);
  }
  return [...names];
}

describe("cn", () => {
  it("lets a later font size override an earlier one without dropping the color", () => {
    expect(cn("text-sm text-text", "text-3xl")).toBe("text-text text-3xl");
    expect(cn("text-sm text-danger", "text-md")).toBe("text-danger text-md");
  });

  it("lets a later color override an earlier one without dropping the size", () => {
    expect(cn("text-text-secondary text-sm", "text-primary")).toBe("text-sm text-primary");
  });

  it("resolves the custom radius, shadow, and z-index tokens", () => {
    expect(cn("rounded-control px-3", "rounded-full")).toBe("px-3 rounded-full");
    expect(cn("rounded-control", "rounded-structural")).toBe("rounded-structural");
    expect(cn("focus:shadow-focus", "focus:shadow-none")).toBe("focus:shadow-none");
    expect(cn("z-dropdown", "z-modal")).toBe("z-modal");
  });

  it("drops falsy entries", () => {
    expect(cn("px-3", false, null, undefined, "py-2")).toBe("px-3 py-2");
  });

  // tokens.css is the source of truth; a token family added there without
  // being taught to cn() would silently stop merging.
  it.each([
    ["radius", RADIUS_TOKENS],
    ["shadow", SHADOW_TOKENS],
    ["z", Z_TOKENS],
  ] as const)("knows every --%s-* token in tokens.css", (prefix, known) => {
    const declared = tokenNames(prefix);
    expect(declared.length).toBeGreaterThan(0);
    expect([...known].sort()).toEqual(expect.arrayContaining(declared.sort()));
  });
});
