import writeXlsxFile from "write-excel-file/browser";
import { leafAccessiblePaths, visibleLeaves } from "./pivot-grid-layout.js";
import type { PivotCell, PivotHeaderNode, PivotValueColumn } from "./pivot-view-types.js";

// The Download button has no access to PivotGrid's own collapse state
// (local useState inside that component, never lifted to props) — the
// export always covers the full underlying data at leaf granularity,
// regardless of what's currently collapsed on screen. Every leaf sits at
// the same depth (mapPivotResponse only ever builds tree nodes down to
// the finest grain the backend returned), so no padding is needed between
// rows.
export function buildPivotSheetData(
  rows: string[],
  rowHeaders: PivotHeaderNode[],
  columnHeaders: PivotHeaderNode[],
  values: PivotValueColumn[],
  cells: PivotCell[],
): (string | number | null)[][] {
  const noCollapse = new Set<string>();
  const rowLeaves = visibleLeaves(rowHeaders, noCollapse);
  const columnLeaves = visibleLeaves(columnHeaders, noCollapse);
  const rowPaths = leafAccessiblePaths(rowHeaders, noCollapse);
  const columnPaths = leafAccessiblePaths(columnHeaders, noCollapse);

  const cellLookup = new Map<string, number | null>();
  for (const cell of cells) cellLookup.set(`${cell.rowKey}|${cell.columnKey}|${cell.valueKey}`, cell.value);

  const rowLabelColumns = rows.length > 0 ? rows : [""];
  const headerRow: (string | number | null)[] = [
    ...rowLabelColumns,
    ...columnLeaves.flatMap((col) =>
      values.map((v) => `${(columnPaths.get(col.key) ?? [col.accessibleLabel]).join(" / ")} — ${v.label}`),
    ),
  ];

  const bodyRows = rowLeaves.map((row) => {
    const path = rowPaths.get(row.key) ?? [row.accessibleLabel];
    const rowCells = columnLeaves.flatMap((col) =>
      values.map((v) => cellLookup.get(`${row.key}|${col.key}|${v.key}`) ?? null),
    );
    return [...path, ...rowCells];
  });

  return [headerRow, ...bodyRows];
}

export async function pivotSheetDataToBlob(sheetData: (string | number | null)[][]): Promise<Blob> {
  return (await writeXlsxFile(sheetData)).toBlob();
}
