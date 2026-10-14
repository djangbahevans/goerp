import { PermissionContext } from "@goerp/sdk/auth";
import { useContext, useMemo, useState } from "react";
import { useConditionEvaluator } from "../../conditions/use-condition-evaluator.js";
import type { ListColumn, ListViewDeclaration } from "./list-view-types.js";

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
  hiddenColumns: ListColumn[];
  revealedFields: ReadonlySet<string>;
  toggleColumn: (field: string) => void;
}

export function useVisibleColumns(view: ListViewDeclaration): VisibleColumnsResult {
  const permissions = useContext(PermissionContext);
  if (!permissions) {
    throw new Error("useVisibleColumns must be used within a PermissionProvider");
  }

  const conditions = useConditionEvaluator(view.name);
  const declaredColumns = view.columns;
  const { check, checkField } = permissions;
  const allColumns = useMemo(
    () =>
      (declaredColumns ?? []).filter(
        (column) =>
          (!column.permission || check(column.permission)) &&
          conditions.isVisible(column.condition, `column "${column.field}" condition`),
      ),
    [declaredColumns, conditions, check],
  );
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
