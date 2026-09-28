import type { APIClient } from "@goerp/sdk";
import { isAppError } from "@goerp/sdk/error";
import type { ActionRoute } from "@goerp/sdk/react";
import type { ModelDef, ResourceRegistryEntry } from "@goerp/sdk/schema";
import type { ListColumn, Row } from "../list/list-view-types.js";
import type { FormField, FormSection } from "./form-view-types.js";

// editable-sub-list.md's non-rendering half: which routes a row writes
// through, what a column edits as, and how a failed save maps onto cells.

export interface WriteRoute {
  method: string;
  path: string;
  permissions: string[];
}

export interface SubListRoutes {
  create: WriteRoute | null;
  update: WriteRoute | null;
  delete: WriteRoute | null;
  previewPath: string | null;
}

// A section's create_route/update_route/delete_route override, else the
// related model's own CRUD route (manifest-spec.md §9.2).
export async function resolveSubListRoutes(
  section: Pick<FormSection, "create_route" | "update_route" | "delete_route">,
  entry: ResourceRegistryEntry,
  model: Pick<ModelDef, "enabled_ops">,
  resolveAction: (routeName: string) => Promise<ActionRoute>,
): Promise<SubListRoutes> {
  const pick = async (
    override: string | undefined,
    path: string | null,
    method: string | null,
    permissions: string[] | null,
  ): Promise<WriteRoute | null> => {
    if (override) return resolveAction(override);
    if (!path || !method || permissions === null) return null;
    return { method, path, permissions };
  };
  const [create, update, del] = await Promise.all([
    pick(section.create_route, entry.createPath, entry.createMethod, entry.createPermissions),
    pick(section.update_route, entry.updatePath, entry.updateMethod, entry.updatePermissions),
    pick(section.delete_route, entry.deletePath, entry.deleteMethod, entry.deletePermissions),
  ]);
  return {
    create,
    update,
    delete: del,
    previewPath: model.enabled_ops.includes("preview") ? entry.previewPath : null,
  };
}

function fillId(path: string, id: string): string {
  return path.replace("{id}", encodeURIComponent(id));
}

type WriteClient = Pick<APIClient, "post" | "put" | "patch" | "delete">;

export function sendWrite<T>(
  client: WriteClient,
  route: WriteRoute,
  id: string | undefined,
  body?: object,
): Promise<T> {
  const path = id === undefined ? route.path : fillId(route.path, id);
  switch (route.method.toUpperCase()) {
    case "PUT":
      return client.put<T>(path, body);
    case "PATCH":
      return client.patch<T>(path, body);
    case "DELETE":
      return client.delete<T>(path);
    default:
      return client.post<T>(path, body);
  }
}

// "sequence ASC" / "sequence DESC" (the manifest's sort spelling) to the
// list API's "sequence" / "-sequence".
export function toSortParam(sort: string | undefined): string | undefined {
  if (!sort) return undefined;
  const [field, dir] = sort.trim().split(/\s+/);
  if (!field) return undefined;
  return dir?.toUpperCase() === "DESC" ? `-${field}` : field;
}

export type CellEditor = { kind: "field"; field: FormField } | { kind: "checkbox" } | { kind: "tags" };

// editable-sub-list.md "Cells in edit mode": a column type's editor, or
// null for a type with none (the cell keeps its view-mode rendering).
export function editorFor(column: ListColumn): CellEditor | null {
  const base = { field: column.field, label: column.label ?? column.field };
  switch (column.type ?? "text") {
    case "text":
    case "email":
    case "phone":
    case "url":
    case "number":
    case "percent":
    case "date":
    case "datetime":
    case "time":
      return { kind: "field", field: { ...base, type: column.type ?? "text" } as FormField };
    case "currency":
      return {
        kind: "field",
        field: {
          ...base,
          type: "currency",
          ...(column.currency_field ? { currency_field: column.currency_field } : {}),
        },
      };
    case "badge":
      return {
        kind: "field",
        field: {
          ...base,
          type: "select",
          options: Object.entries(column.badge_config ?? {}).map(([value, badge]) => ({ value, label: badge.label })),
        },
      };
    // A list row holds a relation column's bare FK id, which is the
    // select-with-resource field's value shape, not "relation"'s embedded
    // {id, display} companion.
    case "relation":
      if (!column.resource) return null;
      return {
        kind: "field",
        field: {
          ...base,
          type: "select",
          resource: column.resource,
          ...(column.resource_label_field ? { resource_label_field: column.resource_label_field } : {}),
        },
      };
    case "country":
      return { kind: "field", field: { ...base, type: "country_select" } };
    case "color":
      return { kind: "field", field: { ...base, type: "color_picker" } };
    case "boolean":
      return { kind: "checkbox" };
    case "tags":
      return { kind: "tags" };
    default:
      return null;
  }
}

export interface SaveErrorMapping {
  cells: Record<string, string>;
  row: string | null;
}

function firstMessage(value: unknown): string | null {
  if (typeof value === "string") return value;
  if (Array.isArray(value)) {
    const first = value.find((item) => typeof item === "string");
    return typeof first === "string" ? first : null;
  }
  return null;
}

// editable-sub-list.md "Content and Edge Cases" error mapping. A message
// for a field that isn't an editable column goes to the row error line,
// prefixed with that field's column label when it has one.
export function mapSaveError(
  err: unknown,
  editableFields: ReadonlySet<string>,
  labelOf: (field: string) => string,
): SaveErrorMapping {
  if (!isAppError(err)) {
    return { cells: {}, row: err instanceof Error && err.message ? err.message : "Couldn't save this row." };
  }
  if (err.isConflict() || err.code.endsWith("etag_mismatch")) {
    return { cells: {}, row: "This row changed since you opened it. Cancel to reload it." };
  }
  const byField: Record<string, string> = {};
  const details = err.details ?? {};
  if (typeof details.field === "string" && err.code.endsWith("validation_failed")) {
    byField[details.field] = err.message;
  } else if (err.isValidation()) {
    for (const [field, value] of Object.entries(details)) {
      const message = firstMessage(value);
      if (message !== null) byField[field] = message;
    }
  }
  const cells: Record<string, string> = {};
  const rowMessages: string[] = [];
  for (const [field, message] of Object.entries(byField)) {
    if (editableFields.has(field)) cells[field] = message;
    else rowMessages.push(`${labelOf(field)}: ${message}`);
  }
  if (Object.keys(byField).length === 0) rowMessages.push(err.message || "Couldn't save this row.");
  return { cells, row: rowMessages.length > 0 ? rowMessages.join(" ") : null };
}

export function isEmptyValue(value: unknown): boolean {
  return value === undefined || value === null || value === "" || (Array.isArray(value) && value.length === 0);
}

// The fields a draft actually changed from its baseline.
export function changedFields(base: Row, edits: Row): Row {
  const out: Row = {};
  for (const [key, value] of Object.entries(edits)) {
    if (!Object.is(value, base[key])) out[key] = value;
  }
  return out;
}
