import { extendTailwindMerge } from "tailwind-merge";

// tailwind-merge's default config already recognizes the design tokens'
// color names and the --text-* size scale. These three token families use
// non-default names it can't infer, so without them e.g. `rounded-control`
// would survive alongside an overriding `rounded-full`.
export const RADIUS_TOKENS = ["control", "structural", "full"] as const;
export const SHADOW_TOKENS = ["focus", "sm", "md", "lg"] as const;
export const Z_TOKENS = ["base", "raised", "dropdown", "sticky", "overlay", "modal", "toast", "tooltip"] as const;

const merge = extendTailwindMerge({
  extend: {
    theme: {
      radius: [...RADIUS_TOKENS],
      shadow: [...SHADOW_TOKENS],
    },
    classGroups: {
      z: [{ z: [...Z_TOKENS] }],
    },
  },
});

// Joins class lists, letting a later utility override an earlier conflicting
// one (e.g. cn(fieldInputClassName(...), "text-3xl") drops the helper's
// text-sm) instead of leaving the winner to stylesheet order.
export function cn(...classes: Array<string | false | null | undefined>): string {
  return merge(...classes);
}
