import { PermissionContext } from "@goerp/sdk/auth";
import { useContext, useMemo, useState } from "react";
import type { ListColumn, ListViewDeclaration } from "./list-view-types.js";

// A column the current user lacks field-level read access to is absent from
// the DOM entirely, not rendered blank — goerp#636's own AC. A manifest
// column marked `hidden: true` is dropped the same way unless its field is
// in `revealedFields` — manifest-spec.md §9.1's "toggleable by the user"
// half of `hidden`, wired up by useVisibleColumns' own toggle state below.
export function filterColumnsByFieldAccess(
  columns: ListColumn[],
  resource: string,
  checkField: (model: string, field: string, mode: "read" | "write") => boolean,
  revealedFields: ReadonlySet<string> = new Set(),
): ListColumn[] {
  return columns.filter(
    (column) => (!column.hidden || revealedFields.has(column.field)) && checkField(resource, column.field, "read"),
  );
}

export interface VisibleColumnsResult {
  columns: ListColumn[];
  // hidden: true columns the user can read at all — candidates for a
  // "Columns" toggle checklist, whether or not they're currently revealed.
  hiddenColumns: ListColumn[];
  revealedFields: ReadonlySet<string>;
  toggleColumn: (field: string) => void;
}

export function useVisibleColumns(view: ListViewDeclaration): VisibleColumnsResult {
  const permissions = useContext(PermissionContext);
  if (!permissions) {
    throw new Error("useVisibleColumns must be used within a PermissionProvider");
  }

  const allColumns = view.columns ?? [];
  const { checkField } = permissions;
  // Session-local only, same as every other per-user display preference
  // ListRenderer already holds (sort, group-by, filters) — none of those
  // persist across a remount either.
  const [revealedFields, setRevealedFields] = useState<ReadonlySet<string>>(() => new Set());

  const columns = useMemo(
    () => filterColumnsByFieldAccess(allColumns, view.resource, checkField, revealedFields),
    [allColumns, view.resource, checkField, revealedFields],
  );

  const hiddenColumns = useMemo(
    () => allColumns.filter((column) => column.hidden && checkField(view.resource, column.field, "read")),
    [allColumns, view.resource, checkField],
  );

  function toggleColumn(field: string): void {
    setRevealedFields((current) => {
      const next = new Set(current);
      if (next.has(field)) next.delete(field);
      else next.add(field);
      return next;
    });
  }

  return { columns, hiddenColumns, revealedFields, toggleColumn };
}
