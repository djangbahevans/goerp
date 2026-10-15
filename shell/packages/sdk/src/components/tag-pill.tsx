import type { ReactNode } from "react";

// Tag colors are arbitrary tenant values: a 6-digit hex fill gets whichever of black or white text
// has the higher WCAG contrast ratio against it.
function pillTextClassFor(color: string | undefined): string {
  const match = color ? /^#?([0-9a-f]{6})$/i.exec(color) : null;
  if (!match) return "text-text";
  const hex = match[1] as string;
  const channel = (start: number) => {
    const c = Number.parseInt(hex.slice(start, start + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  const luminance = 0.2126 * channel(0) + 0.7152 * channel(2) + 0.0722 * channel(4);
  const whiteContrast = 1.05 / (luminance + 0.05);
  const blackContrast = (luminance + 0.05) / 0.05;
  return blackContrast >= whiteContrast ? "text-black" : "text-white";
}

export interface TagPillProps {
  name: string;
  // Any CSS color; the label switches between black and white text for contrast with a hex fill.
  color?: string | undefined;
  className?: string | undefined;
  // Rendered after the name, such as a remove button.
  children?: ReactNode;
}

// A tag rendered in its own color, or as a neutral pill when it has none.
export function TagPill({ name, color, className = "px-2 py-0.5", children }: TagPillProps): ReactNode {
  return (
    <span
      style={color ? { backgroundColor: color } : undefined}
      // The outline keeps a fill that matches the surrounding background visible.
      className={`inline-flex items-center gap-1 rounded-full text-sm ${className} ${
        color ? `border border-border ${pillTextClassFor(color)}` : "bg-bg-subtle text-text-secondary"
      }`}
    >
      {name}
      {children}
    </span>
  );
}
