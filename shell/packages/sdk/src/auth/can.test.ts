import { describe, expect, it } from "vitest";
import { shouldRenderCan } from "./can.js";

describe("shouldRenderCan", () => {
  it("renders children when allowed and not inverted", () => {
    expect(shouldRenderCan(true, false)).toBe(true);
  });

  it("renders fallback when denied and not inverted", () => {
    expect(shouldRenderCan(false, false)).toBe(false);
  });

  it("renders fallback when allowed but inverted", () => {
    expect(shouldRenderCan(true, true)).toBe(false);
  });

  it("renders children when denied and inverted", () => {
    expect(shouldRenderCan(false, true)).toBe(true);
  });
});
