import { describe, expect, it } from "vitest";
import { contrastFor } from "./contrast.js";

describe("contrastFor", () => {
  it("picks inverse (light) text for a dark, saturated fill in light theme", () => {
    expect(contrastFor("#1E293B", "light").text).toEqual({ kind: "token", className: "text-text-inverse" });
  });

  it("picks regular text for a light, pale fill in light theme", () => {
    expect(contrastFor("#FDE68A", "light").text).toEqual({ kind: "token", className: "text-text" });
  });

  it("picks inverse (dark) text for a light, pale fill in dark theme", () => {
    expect(contrastFor("#FDE68A", "dark").text).toEqual({ kind: "token", className: "text-text-inverse" });
  });

  it("flags a near-white fill as needing an outline against the light-theme surface", () => {
    expect(contrastFor("#FAFAFA", "light").needsOutline).toBe(true);
  });

  it("does not flag a saturated fill that already clears the fill-vs-surface minimum", () => {
    expect(contrastFor("#3B82F6", "light").needsOutline).toBe(false);
  });

  it("flags a near-black fill as needing an outline against the dark-theme surface", () => {
    expect(contrastFor("#0A0C10", "dark").needsOutline).toBe(true);
  });

  it("falls back to default text with no outline for a value that isn't a valid hex", () => {
    expect(contrastFor("blue", "light")).toEqual({
      text: { kind: "token", className: "text-text" },
      needsOutline: false,
    });
  });

  it("accepts a hex value without a leading #", () => {
    expect(contrastFor("1E293B", "light").text).toEqual({ kind: "token", className: "text-text-inverse" });
  });

  it("falls back to literal black/white when neither themed text token clears 4.5:1 (light theme)", () => {
    // Neither --color-text (#10141C) nor --color-text-inverse (#FFFFFF)
    // clears 4.5:1 against this mid-gray fill (~4.3:1 best case) — the
    // theme's tokens are a muted graphite/off-white, not literal
    // black/white, so this narrow luminance band needs the literal
    // fallback to actually meet WCAG AA.
    expect(contrastFor("#7A7A7A", "light").text).toEqual({ kind: "literal", hex: "#000000" });
  });

  it("falls back to literal black/white when neither themed text token clears 4.5:1 (dark theme)", () => {
    expect(contrastFor("#E7002D", "dark").text).toEqual({ kind: "literal", hex: "#FFFFFF" });
  });

  it("defaults to standard contrast (4.5:1) when no contrast level is passed", () => {
    expect(contrastFor("#3B82F6", "light")).toEqual(contrastFor("#3B82F6", "light", "standard"));
  });

  it("holds a fill to 7:1 in high contrast even though it clears 4.5:1 in standard", () => {
    // #5A5A5A's best themed-token contrast is ~6.9:1 — clears standard's
    // 4.5:1 bar but falls just short of high contrast's 7:1 AAA bar.
    const standard = contrastFor("#5A5A5A", "light", "standard");
    const high = contrastFor("#5A5A5A", "light", "high");
    expect(standard.text.kind).toBe("token");
    expect(high.text.kind).toBe("literal");
  });

  it("picks literal black text in high-contrast dark mode when neither pure black nor white clears 7:1", () => {
    expect(contrastFor("#7A7A7A", "dark", "high").text.kind).toBe("literal");
  });
});
