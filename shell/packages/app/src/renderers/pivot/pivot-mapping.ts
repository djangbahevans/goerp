import type { PivotCellResponse, PivotResponse } from "@goerp/sdk/react";
import type { PivotValue } from "./pivot-manifest-types.js";
import type { PivotCell, PivotHeaderNode, PivotValueColumn } from "./pivot-view-types.js";

// A response cell's row/column arrays carry a trailing run of `null`s for
// every axis position rolled up at or above that cell (dispatchORMPivot's
// GROUPING()-driven contract, view-system.md §8). The non-null prefix is
// the path of the PivotHeaderNode this cell's value belongs to — full
// length for a leaf group, shorter for a collapsed ancestor's own
// subtotal. An empty prefix on a non-empty axis (every position rolled
// up, including the overall grand total) has no header node to attach to
// in this primitive's tree-of-arrays shape (no synthetic "grand total"
// root — pivot-grid-layout.ts's layoutHeaderLevels only ever walks the
// nodes it's given), so it's dropped rather than fabricated a home.
function activePrefix(path: (string | number | boolean | null)[]): (string | number | boolean)[] | undefined {
  const end = path.indexOf(null);
  const prefix = end === -1 ? path : path.slice(0, end);
  // A `null` before the end (a rolled-up level with a real value again
  // afterward) never happens — ROLLUP only ever rolls up a *suffix* of
  // its grouping columns — but guarded rather than assumed.
  if (path.slice(prefix.length).some((v) => v !== null)) return undefined;
  return prefix as (string | number | boolean)[];
}

function pathKey(prefix: (string | number | boolean)[]): string {
  return JSON.stringify(prefix);
}

// Builds one axis's header tree from the set of fully-resolved (no
// rolled-up position) paths across every response cell — every
// intermediate node a rollup subtotal could reference is guaranteed to
// appear as some leaf path's own prefix, since ROLLUP(a,b) always
// includes the (a,b) finest grain alongside every coarser level for the
// same underlying rows.
function buildHeaderTree(leafPaths: (string | number | boolean)[][]): PivotHeaderNode[] {
  const roots: PivotHeaderNode[] = [];
  const byKey = new Map<string, PivotHeaderNode>();

  for (const path of leafPaths) {
    let siblings = roots;
    let keyPrefix: (string | number | boolean)[] = [];
    for (const segment of path) {
      keyPrefix = [...keyPrefix, segment];
      const key = pathKey(keyPrefix);
      let node = byKey.get(key);
      if (!node) {
        const label = String(segment);
        node = { key, label, accessibleLabel: label };
        byKey.set(key, node);
        siblings.push(node);
      }
      node.children ??= [];
      siblings = node.children;
    }
  }

  // A depth-1 axis (single-field rows/columns) never gains a `children`
  // array above — PivotHeaderNode's own contract treats undefined and
  // empty-array children as equivalent leaves, so this is cosmetic, but
  // strips it for a cleaner fixture/snapshot shape.
  function pruneEmptyChildren(nodes: PivotHeaderNode[]): void {
    for (const node of nodes) {
      if (node.children && node.children.length === 0) delete node.children;
      else if (node.children) pruneEmptyChildren(node.children);
    }
  }
  pruneEmptyChildren(roots);

  return roots;
}

function toCellValue(raw: number | string | null): number | null {
  if (raw === null) return null;
  const n = Number(raw);
  return Number.isNaN(n) ? null : n;
}

// A manifest declaring zero fields on an axis ("total by row, no column
// breakdown") means every cell's prefix on that axis is always the empty
// path — buildHeaderTree never sees a leaf path to build a node from, so
// rowHeaders/columnHeaders would come back genuinely empty. PivotGrid
// treats an empty axis as "no data" (pivot-grid.tsx's own empty-state
// check), not "no breakdown requested" — a real difference PivotGrid has
// no other way to tell apart, so a single implicit Total node stands in
// for the declared-empty axis instead, keyed identically to how every
// cell on that axis already resolves its own key (pathKey([]) === "[]").
function totalNode(): PivotHeaderNode {
  return { key: "[]", label: "Total", accessibleLabel: "Total" };
}

export interface MappedPivotData {
  rowHeaders: PivotHeaderNode[];
  columnHeaders: PivotHeaderNode[];
  cells: PivotCell[];
}

// Resolves the backend's flat, ROLLUP-shaped PivotResponse (view-system.md
// §8's use_wasm: false contract) into the PivotGrid primitive's
// props (pivot-view-types.ts) — the concern PR #755 (goerp#718) explicitly
// deferred to goerp#646.
export function mapPivotResponse(response: PivotResponse, rows: string[], columns: string[]): MappedPivotData {
  const rowLeafPaths: (string | number | boolean)[][] = [];
  const columnLeafPaths: (string | number | boolean)[][] = [];
  const cellEntries: { rowKey: string; columnKey: string; values: PivotCellResponse["values"] }[] = [];

  for (const cell of response.cells) {
    const rowPrefix = activePrefix(cell.row);
    const columnPrefix = activePrefix(cell.column);
    if (rowPrefix === undefined || columnPrefix === undefined) continue;
    if (rows.length > 0 && rowPrefix.length === 0) continue;
    if (columns.length > 0 && columnPrefix.length === 0) continue;

    if (rowPrefix.length === rows.length && rows.length > 0) rowLeafPaths.push(rowPrefix);
    if (columnPrefix.length === columns.length && columns.length > 0) columnLeafPaths.push(columnPrefix);

    cellEntries.push({ rowKey: pathKey(rowPrefix), columnKey: pathKey(columnPrefix), values: cell.values });
  }

  const rowHeaders = rows.length === 0 ? [totalNode()] : buildHeaderTree(rowLeafPaths);
  const columnHeaders = columns.length === 0 ? [totalNode()] : buildHeaderTree(columnLeafPaths);

  const cells: PivotCell[] = cellEntries.flatMap(({ rowKey, columnKey, values }) =>
    Object.entries(values).map(([valueKey, raw]) => ({ rowKey, columnKey, valueKey, value: toCellValue(raw) })),
  );

  return { rowHeaders, columnHeaders, cells };
}

export function toValueColumns(values: PivotValue[]): PivotValueColumn[] {
  return values.map((v) => ({
    key: `${v.field}_${v.aggregation}`,
    label: v.label ?? v.field,
    ...(v.format !== undefined ? { format: v.format } : {}),
  }));
}
