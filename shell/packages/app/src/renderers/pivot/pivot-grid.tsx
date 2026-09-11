import { EmptyState, formatFieldValue, Skeleton, Spinner } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import { useMemo, useState } from "react";
import { layoutHeaderLevels, leafAccessiblePaths, visibleLeaves } from "./pivot-grid-layout.js";
import type { PivotGridProps, PivotValueColumn } from "./pivot-view-types.js";

// A fixed sticky-column width is far simpler than measuring real widths
// (ResizeObserver), and the doc leaves the visual treatment unspecified.
const ROW_HEADER_COLUMN_WIDTH = 160;

interface CellKey {
  rowKey: string;
  columnKey: string;
  valueKey: string;
}

// JSON-encoded rather than delimiter-joined, since caller-supplied keys
// (arbitrary path strings) aren't guaranteed to exclude any fixed separator.
function cellMapKey({ rowKey, columnKey, valueKey }: CellKey): string {
  return JSON.stringify([rowKey, columnKey, valueKey]);
}

function toggle(set: ReadonlySet<string>, key: string): Set<string> {
  const next = new Set(set);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  return next;
}

function CollapseToggle({
  expanded,
  label,
  onToggle,
}: {
  expanded: boolean;
  label: string;
  onToggle: () => void;
}): ReactNode {
  return (
    <button
      type="button"
      aria-expanded={expanded}
      aria-label={expanded ? `Collapse ${label}` : `Expand ${label}`}
      onClick={onToggle}
      className="mr-1 inline-flex h-4 w-4 items-center justify-center rounded-control text-text-secondary hover:bg-surface-hover focus-visible:shadow-focus focus-visible:outline-none"
    >
      {expanded ? "−" : "+"}
    </button>
  );
}

export function PivotGrid({
  rowHeaders,
  columnHeaders,
  values,
  cells,
  isLoading = false,
  isRecomputing = false,
  emptyState,
}: PivotGridProps): ReactNode {
  const [collapsedRows, setCollapsedRows] = useState<Set<string>>(new Set());
  const [collapsedColumns, setCollapsedColumns] = useState<Set<string>>(new Set());
  const [hoveredRowLeaf, setHoveredRowLeaf] = useState<string | null>(null);
  const [hoveredColumnLeaf, setHoveredColumnLeaf] = useState<string | null>(null);
  const [isScrolled, setIsScrolled] = useState(false);

  const rowLevels = useMemo(() => layoutHeaderLevels(rowHeaders, collapsedRows), [rowHeaders, collapsedRows]);
  const columnLevels = useMemo(
    () => layoutHeaderLevels(columnHeaders, collapsedColumns),
    [columnHeaders, collapsedColumns],
  );
  // layoutHeaderLevels already computes tree depth internally — one array
  // per level — so its own length is the depth, with no second walk.
  const rowDepth = rowLevels.length;
  const columnDepth = columnLevels.length;
  const rowLeaves = useMemo(() => visibleLeaves(rowHeaders, collapsedRows), [rowHeaders, collapsedRows]);
  const columnLeaves = useMemo(() => visibleLeaves(columnHeaders, collapsedColumns), [columnHeaders, collapsedColumns]);
  const rowPaths = useMemo(() => leafAccessiblePaths(rowHeaders, collapsedRows), [rowHeaders, collapsedRows]);
  const columnPaths = useMemo(
    () => leafAccessiblePaths(columnHeaders, collapsedColumns),
    [columnHeaders, collapsedColumns],
  );
  // Per level, the cell (if any) that starts at a given row index — an O(1)
  // alternative to scanning each level's array for every tbody row.
  const rowLevelsByStartIndex = useMemo(
    () => rowLevels.map((level) => new Map(level.map((cell) => [cell.startIndex, cell]))),
    [rowLevels],
  );

  const cellMap = useMemo(() => {
    const map = new Map<string, number | null>();
    for (const cell of cells) map.set(cellMapKey(cell), cell.value);
    return map;
  }, [cells]);

  if (isLoading) {
    return <Skeleton type="table" rows={6} columns={4} />;
  }

  if (rowHeaders.length === 0 || columnHeaders.length === 0 || values.length === 0) {
    return (
      emptyState ?? <EmptyState title="No results" description="No data matches the current rows/columns/filters." />
    );
  }

  const showValueSubHeader = values.length > 1;
  const headerRowCount = columnDepth + (showValueSubHeader ? 1 : 0);
  const rowHeaderColumnLeft = (depth: number): number => depth * ROW_HEADER_COLUMN_WIDTH;
  const stickyShadowClassName = isScrolled ? "shadow-sm" : "";

  function isRowHeaderHighlighted(leafKeys: string[]): boolean {
    return hoveredRowLeaf !== null && leafKeys.includes(hoveredRowLeaf);
  }
  function isColumnHeaderHighlighted(leafKeys: string[]): boolean {
    return hoveredColumnLeaf !== null && leafKeys.includes(hoveredColumnLeaf);
  }

  return (
    <div className="rounded-structural border border-border">
      <div className="relative overflow-x-auto" onScroll={(event) => setIsScrolled(event.currentTarget.scrollLeft > 0)}>
        <table className="w-full border-collapse">
          <colgroup>
            {Array.from({ length: rowDepth }, (_, depthIndex) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: depth is a stable structural position.
              <col key={`row-header-col-${depthIndex}`} style={{ width: ROW_HEADER_COLUMN_WIDTH }} />
            ))}
            {columnLeaves.flatMap((leaf) =>
              values.map((value) => <col key={`${leaf.key}:${value.key}`} className="min-w-24" />),
            )}
          </colgroup>
          <thead>
            {columnLevels.map((level, depth) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: depth is a stable structural position, not row identity.
              <tr key={depth}>
                {depth === 0 &&
                  Array.from({ length: rowDepth }, (_, rowDepthIndex) => (
                    <th
                      // biome-ignore lint/suspicious/noArrayIndexKey: a fixed-size placeholder grid of depth-column spacers with no identity of its own to key by.
                      key={`corner-${rowDepthIndex}`}
                      scope="col"
                      rowSpan={headerRowCount}
                      style={{ position: "sticky", left: rowHeaderColumnLeft(rowDepthIndex), zIndex: 3 }}
                      className={`min-w-40 border-border border-b bg-surface p-3 ${
                        rowDepthIndex === rowDepth - 1 ? stickyShadowClassName : ""
                      }`}
                    />
                  ))}
                {level.map((cell) => (
                  <th
                    key={cell.node.key}
                    scope="colgroup"
                    colSpan={cell.span * (showValueSubHeader ? values.length : 1)}
                    rowSpan={cell.crossSpan}
                    className={`border-border border-b p-3 text-left font-medium text-sm text-text-secondary ${
                      isColumnHeaderHighlighted(cell.leafKeys) ? "bg-surface-hover" : "bg-surface"
                    }`}
                  >
                    {cell.hasChildren && (
                      <CollapseToggle
                        expanded={!cell.isCollapsed}
                        label={cell.node.accessibleLabel}
                        onToggle={() => setCollapsedColumns((current) => toggle(current, cell.node.key))}
                      />
                    )}
                    {cell.node.label}
                  </th>
                ))}
              </tr>
            ))}
            {showValueSubHeader && (
              <tr>
                {columnLeaves.flatMap((leaf) =>
                  values.map((value) => (
                    <th
                      key={`${leaf.key}:${value.key}`}
                      scope="col"
                      className="border-border border-b bg-surface p-3 text-right font-medium text-sm text-text-secondary"
                    >
                      {value.label}
                    </th>
                  )),
                )}
              </tr>
            )}
          </thead>
          <tbody className={isRecomputing ? "opacity-60" : ""}>
            {rowLeaves.map((rowLeaf, rowIndex) => (
              <tr key={rowLeaf.key}>
                {rowLevelsByStartIndex.map((level, depth) => {
                  const cell = level.get(rowIndex);
                  if (!cell) return null;
                  return (
                    <th
                      key={cell.node.key}
                      scope="row"
                      rowSpan={cell.span}
                      colSpan={cell.crossSpan}
                      style={{ position: "sticky", left: rowHeaderColumnLeft(depth), zIndex: 2 }}
                      className={`border-border border-b p-3 text-left font-medium text-sm text-text-secondary ${
                        isRowHeaderHighlighted(cell.leafKeys) ? "bg-surface-hover" : "bg-surface"
                      } ${depth + cell.crossSpan >= rowDepth ? stickyShadowClassName : ""}`}
                    >
                      {cell.hasChildren && (
                        <CollapseToggle
                          expanded={!cell.isCollapsed}
                          label={cell.node.accessibleLabel}
                          onToggle={() => setCollapsedRows((current) => toggle(current, cell.node.key))}
                        />
                      )}
                      {cell.node.label}
                    </th>
                  );
                })}
                {columnLeaves.map((columnLeaf) =>
                  values.map((value) => {
                    const raw =
                      cellMap.get(
                        cellMapKey({ rowKey: rowLeaf.key, columnKey: columnLeaf.key, valueKey: value.key }),
                      ) ?? null;
                    return (
                      <PivotCellDisplay
                        key={`${rowLeaf.key}:${columnLeaf.key}:${value.key}`}
                        rawValue={raw}
                        value={value}
                        rowPath={rowPaths.get(rowLeaf.key) ?? []}
                        columnPath={columnPaths.get(columnLeaf.key) ?? []}
                        isHighlighted={hoveredRowLeaf === rowLeaf.key || hoveredColumnLeaf === columnLeaf.key}
                        onHover={() => {
                          setHoveredRowLeaf(rowLeaf.key);
                          setHoveredColumnLeaf(columnLeaf.key);
                        }}
                        onUnhover={() => {
                          setHoveredRowLeaf(null);
                          setHoveredColumnLeaf(null);
                        }}
                      />
                    );
                  }),
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {isRecomputing && (
        <div role="status" className="flex items-center gap-2 border-border border-t p-2 text-text-secondary text-xs">
          <Spinner size={14} />
          Recomputing…
        </div>
      )}
    </div>
  );
}

function PivotCellDisplay({
  rawValue,
  value,
  rowPath,
  columnPath,
  isHighlighted,
  onHover,
  onUnhover,
}: {
  rawValue: number | null;
  value: PivotValueColumn;
  rowPath: string[];
  columnPath: string[];
  isHighlighted: boolean;
  onHover: () => void;
  onUnhover: () => void;
}): ReactNode {
  const formatted = formatFieldValue(rawValue, value.format ?? "number", value.currency, "—");
  const context = [...rowPath, ...columnPath, value.label].join(", ");

  return (
    <td
      onMouseEnter={onHover}
      onMouseLeave={onUnhover}
      className={`border-border border-b p-3 text-right font-mono text-base text-text ${isHighlighted ? "bg-surface-hover" : ""}`}
    >
      <span className="sr-only">{context}: </span>
      {formatted}
    </td>
  );
}
