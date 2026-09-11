import type { LucideProps } from "lucide-react";
import { DynamicIcon, dynamicIconImports } from "lucide-react/dynamic";
import type { ReactNode } from "react";

export type IconName = keyof typeof dynamicIconImports;

export function isKnownIconName(name: string): name is IconName {
  return name in dynamicIconImports;
}

export interface IconProps extends LucideProps {
  // Lucide icon name, e.g. "shopping-cart" (manifest-spec.md's kebab-case
  // convention). An unrecognized name renders nothing rather than throwing.
  name: string;
}

// Resolves a manifest-sourced icon name to its real glyph, code-split per
// icon (lucide-react's own DynamicIcon) rather than statically bundling
// all ~1500 icons into every @goerp/sdk consumer. Renders nothing while
// the icon's chunk loads and nothing at all for an unrecognized name — no
// Suspense boundary needed, DynamicIcon manages its own loading state.
export function Icon({ name, ...props }: IconProps): ReactNode {
  if (!isKnownIconName(name)) return null;
  return <DynamicIcon name={name} {...props} />;
}
