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

export function Skeleton({ type, lines = 3, rows = 3, columns = 3 }: SkeletonProps): ReactNode {
  if (type === "card") {
    return <div data-skeleton="card" aria-hidden="true" />;
  }

  if (type === "table") {
    return (
      <div data-skeleton="table" aria-hidden="true">
        {Array.from({ length: rows }, (_, row) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: a fixed-size placeholder grid with no identity of its own to key by.
          <div key={row} data-skeleton-row="">
            {Array.from({ length: columns }, (_, column) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: see above.
              <span key={column} data-skeleton-cell="" />
            ))}
          </div>
        ))}
      </div>
    );
  }

  return (
    <div data-skeleton="lines" aria-hidden="true">
      {Array.from({ length: lines }, (_, line) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: a fixed-size placeholder grid with no identity of its own to key by.
        <span key={line} data-skeleton-line="" />
      ))}
    </div>
  );
}
