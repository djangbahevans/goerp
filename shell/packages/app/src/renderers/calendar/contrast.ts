import type { Theme } from "@goerp/sdk/react";

// shell-visual-design.md's real per-theme hex values for --color-text /
// --color-text-inverse / --color-surface (tokens.css / themes/*.css).
// EventChip fills are arbitrary tenant-supplied hex, not a themed CSS
// custom property, so the WCAG math below needs concrete numbers to
// compare against rather than a getComputedStyle read.
const THEME_TOKENS: Record<Theme, { text: string; textInverse: string; surface: string }> = {
  light: { text: "#10141C", textInverse: "#FFFFFF", surface: "#FFFFFF" },
  dark: { text: "#E7E9ED", textInverse: "#10141C", surface: "#1A1E26" },
};

const MIN_FILL_CONTRAST = 3;
const MIN_TEXT_CONTRAST = 4.5;

const HEX_RE = /^#?([0-9a-f]{6})$/i;

function parseHex(hex: string): [number, number, number] | null {
  const match = HEX_RE.exec(hex);
  if (!match) return null;
  const value = match[1] as string;
  return [
    Number.parseInt(value.slice(0, 2), 16),
    Number.parseInt(value.slice(2, 4), 16),
    Number.parseInt(value.slice(4, 6), 16),
  ];
}

function srgbChannel(channel255: number): number {
  const c = channel255 / 255;
  return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

// WCAG 2.x relative luminance.
function relativeLuminance(rgb: [number, number, number]): number {
  const [r, g, b] = rgb;
  return 0.2126 * srgbChannel(r) + 0.7152 * srgbChannel(g) + 0.0722 * srgbChannel(b);
}

// WCAG 2.x contrast ratio.
function contrastRatio(a: number, b: number): number {
  const lighter = Math.max(a, b);
  const darker = Math.min(a, b);
  return (lighter + 0.05) / (darker + 0.05);
}

// Text color is normally a themed token (stays correct if the theme's
// exact hex values change); only falls back to a literal black/white when
// neither token clears 4.5:1 against the fill (see contrastFor's comment).
export type EventChipTextColor =
  | { kind: "token"; className: "text-text" | "text-text-inverse" }
  | { kind: "literal"; hex: "#000000" | "#FFFFFF" };

export interface EventChipContrast {
  text: EventChipTextColor;
  needsOutline: boolean;
}

const DEFAULT_CONTRAST: EventChipContrast = { text: { kind: "token", className: "text-text" }, needsOutline: false };

// Picks --color-text or --color-text-inverse, whichever clears 4.5:1
// against `fillHex`, falling back to literal black/white for the narrow
// band of fills neither muted token clears on its own.
// Also flags whether `fillHex` needs a --color-border outline to clear
// the 3:1 non-text minimum against the grid surface.
export function contrastFor(fillHex: string, theme: Theme): EventChipContrast {
  const fillRgb = parseHex(fillHex);
  if (!fillRgb) return DEFAULT_CONTRAST;

  const tokens = THEME_TOKENS[theme];
  const fillLuminance = relativeLuminance(fillRgb);
  const textLuminance = relativeLuminance(parseHex(tokens.text) as [number, number, number]);
  const inverseLuminance = relativeLuminance(parseHex(tokens.textInverse) as [number, number, number]);
  const textContrast = contrastRatio(fillLuminance, textLuminance);
  const inverseContrast = contrastRatio(fillLuminance, inverseLuminance);

  const text: EventChipTextColor =
    Math.max(textContrast, inverseContrast) >= MIN_TEXT_CONTRAST
      ? { kind: "token", className: inverseContrast >= textContrast ? "text-text-inverse" : "text-text" }
      : {
          kind: "literal",
          hex: contrastRatio(fillLuminance, 1) >= contrastRatio(fillLuminance, 0) ? "#FFFFFF" : "#000000",
        };

  const surfaceLuminance = relativeLuminance(parseHex(tokens.surface) as [number, number, number]);
  const needsOutline = contrastRatio(fillLuminance, surfaceLuminance) < MIN_FILL_CONTRAST;

  return { text, needsOutline };
}
