import { PermissionContext } from "@goerp/sdk/auth";
import { useContext, useMemo } from "react";
import type { ListColumn, ListViewDeclaration } from "./list-view-types.js";

// A column the current user lacks field-level read access to is absent
// from the DOM entirely, not rendered blank — goerp#636's own AC.
export function filterColumnsByFieldAccess(
  columns: ListColumn[],
  resource: string,
  checkField: (model: string, field: string, mode: "read" | "write") => boolean,
): ListColumn[] {
  return columns.filter((column) => checkField(resource, column.field, "read"));
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
