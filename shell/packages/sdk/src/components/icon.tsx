import type { LucideProps } from "lucide-react";
import { DynamicIcon, dynamicIconImports } from "lucide-react/dynamic";
import type { ReactNode } from "react";

export type IconName = keyof typeof dynamicIconImports;

// Editor autocomplete/typo-catching for a hardcoded name (e.g. a story's
// icon: "check") without rejecting a manifest-sourced string absent from
// this list at compile time — `& {}` keeps the union's literals in
// completions while still widening to plain `string`.
export type IconNameLike = IconName | (string & {});

export function isKnownIconName(name: string): name is IconName {
  return name in dynamicIconImports;
}

export interface IconProps extends LucideProps {
  // Lucide icon name, e.g. "shopping-cart" (manifest-spec.md's kebab-case
  // convention). An unrecognized name renders nothing rather than throwing.
  name: IconNameLike;
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
