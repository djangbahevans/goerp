import { PermissionContext } from "@goerp/sdk/auth";
import { useContext, useMemo } from "react";
import type { ListColumn, ListViewDeclaration } from "./list-view-types.js";

// A column the current user lacks field-level read access to is absent
// from the DOM entirely, not rendered blank — goerp#636's own AC. A
// manifest column marked `hidden: true` is dropped the same way — the
// "toggleable by the user" half of that field (manifest-spec.md §9.1)
// has no UI yet, so it's simply always hidden rather than never hidden.
export function filterColumnsByFieldAccess(
  columns: ListColumn[],
  resource: string,
  checkField: (model: string, field: string, mode: "read" | "write") => boolean,
): ListColumn[] {
  return columns.filter((column) => !column.hidden && checkField(resource, column.field, "read"));
}

export function useVisibleColumns(view: ListViewDeclaration): ListColumn[] {
  const permissions = useContext(PermissionContext);
  if (!permissions) {
    throw new Error("useVisibleColumns must be used within a PermissionProvider");
  }

  const columns = view.columns ?? [];
  const { checkField } = permissions;
  return useMemo(
    () => filterColumnsByFieldAccess(columns, view.resource, checkField),
    [columns, view.resource, checkField],
  );
}
