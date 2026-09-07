import type { ReactNode } from "react";

export type SkeletonType = "card" | "table";

export interface SkeletonProps {
  // Omitted: a set of placeholder line bars (the default mode), sized by
  // `lines`. "card"/"table" are distinct shapes, not a line count.
  type?: SkeletonType | undefined;
  lines?: number | undefined;
  rows?: number | undefined;
  columns?: number | undefined;
}

// docs/components/skeleton.md "Tokens Used" — a quiet opacity pulse (not a
// shimmer sweep) on a --color-border fill, --duration-slower per cycle,
// static under prefers-reduced-motion.
const SHAPE_CLASSES = "rounded-structural bg-border animate-pulse motion-reduce:animate-none";
const SHAPE_STYLE = { animationDuration: "var(--duration-slower)" };

export function Skeleton({ type, lines = 3, rows = 3, columns = 3 }: SkeletonProps): ReactNode {
  if (type === "card") {
    // No documented usage shows a card count or fixed height yet (open
    // question in skeleton.md) — a single ~128px placeholder in the
    // meantime, sized like a generic StatCard-shaped block.
    return (
      <div data-skeleton="card" aria-busy="true">
        <div aria-hidden="true" className={`h-32 ${SHAPE_CLASSES}`} style={SHAPE_STYLE} />
      </div>
    );
  }

  if (type === "table") {
    return (
      <div data-skeleton="table" aria-busy="true" className="flex flex-col gap-2">
        {Array.from({ length: rows }, (_, row) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: a fixed-size placeholder grid with no identity of its own to key by.
          <div key={row} data-skeleton-row="" className="flex gap-2">
            {Array.from({ length: columns }, (_, column) => (
              <span
                // biome-ignore lint/suspicious/noArrayIndexKey: see above.
                key={column}
                data-skeleton-cell=""
                aria-hidden="true"
                className={`h-10 flex-1 ${SHAPE_CLASSES}`}
                style={SHAPE_STYLE}
              />
            ))}
          </div>
        ))}
      </div>
    );
  }

  return (
    <div data-skeleton="lines" aria-busy="true" className="flex flex-col gap-2">
      {Array.from({ length: lines }, (_, line) => (
        <span
          // biome-ignore lint/suspicious/noArrayIndexKey: a fixed-size placeholder grid with no identity of its own to key by.
          key={line}
          data-skeleton-line=""
          aria-hidden="true"
          className={`h-4 ${SHAPE_CLASSES} ${line === lines - 1 ? "w-3/5" : "w-full"}`}
          style={SHAPE_STYLE}
        />
      ))}
    </div>
  );
}
