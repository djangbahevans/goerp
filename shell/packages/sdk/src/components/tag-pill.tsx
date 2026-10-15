import type { ReactNode } from "react";

const HEX_COLOR = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i;

// Tag colors are arbitrary tenant values, so the text takes whichever of black or white has the
// higher WCAG contrast ratio against the fill; at least 4.58:1 against any color.
function textClassFor(hex: string): string {
  const digits = hex.length === 4 ? [...hex.slice(1)].map((d) => d + d).join("") : hex.slice(1);
  const channel = (start: number) => {
    const c = Number.parseInt(digits.slice(start, start + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  const luminance = 0.2126 * channel(0) + 0.7152 * channel(2) + 0.0722 * channel(4);
  return (luminance + 0.05) / 0.05 >= 1.05 / (luminance + 0.05) ? "text-black" : "text-white";
}

const SIZE_CLASSES = {
  sm: "px-2 py-0.5 text-xs",
  md: "p-2 text-sm max-md:min-h-11",
} as const;

export interface TagPillProps {
  name: string;
  // A hex color (#rgb or #rrggbb) fills the pill; any other value renders the neutral pill, since
  // readable text can only be guaranteed against a known fill.
  color?: string | undefined;
  // "sm" matches Badge for read-only display; "md" fits a control inside the pill.
  size?: keyof typeof SIZE_CLASSES | undefined;
  // Rendered after the name, such as a remove button.
  children?: ReactNode;
}

export function TagPill({ name, color, size = "sm", children }: TagPillProps): ReactNode {
  const fill = color !== undefined && HEX_COLOR.test(color) ? color : undefined;
  return (
    <span
      style={fill ? { backgroundColor: fill } : undefined}
      // The outline keeps a fill that matches the surrounding background visible.
      className={`inline-flex items-center gap-1 rounded-full font-medium ${SIZE_CLASSES[size]} ${
        fill ? `border border-border ${textClassFor(fill)}` : "bg-bg-subtle text-text-secondary"
      }`}
    >
      {name}
      {children}
    </span>
  );
}
