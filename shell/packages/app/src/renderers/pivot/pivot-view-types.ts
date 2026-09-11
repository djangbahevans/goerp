// Presentational props — already-resolved header trees and aggregated
// cells, not view-system.md §8's manifest fields, which is goerp#646/#648's
// concern.

import type { ReactNode } from "react";

export type PivotValueFormat = "currency" | "percent" | "number";

export interface PivotValueColumn {
  key: string;
  label: string;
  // "currency": integer minor units (l10n-guide.md). "percent": a fraction
  // (0.42, not 42).
  format?: PivotValueFormat | undefined;
  currency?: string | undefined;
}

// `key` is the node's full path key (e.g. "confirmed.acme_corp") — the
// caller owns the scheme, as with KanbanCardData/CalendarEvent.
export interface PivotHeaderNode {
  key: string;
  label: ReactNode;
  // Plain-text form of `label`, for the cell context announcement — mirrors
  // KanbanCardData's title/accessibleTitle split.
  accessibleLabel: string;
  children?: PivotHeaderNode[] | undefined;
}

// Addressed by a (row node, column node) pair at whatever depth is
// currently visible; a collapsed group still needs its own subtotal here.
export interface PivotCell {
  rowKey: string;
  columnKey: string;
  valueKey: string;
  value: number | null;
}

export interface PivotGridProps {
  rowHeaders: PivotHeaderNode[];
  columnHeaders: PivotHeaderNode[];
  values: PivotValueColumn[];
  cells: PivotCell[];
  isLoading?: boolean | undefined;
  // use_wasm: false's inline body indicator during recompute; a
  // use_wasm: true caller never sets this (its recompute is synchronous).
  isRecomputing?: boolean | undefined;
  emptyState?: ReactNode | undefined;
}

export interface PivotViewProps extends PivotGridProps {
  title?: string | undefined;
  allowDownload?: boolean | undefined;
  onDownload?: (() => void) | undefined;
}
