import { type FieldDef, ViewRegistryContext } from "@goerp/sdk/schema";
import { useContext } from "react";
import type { Row } from "./list/list-view-types.js";

const EMPTY_FIELDS: FieldDef[] = [];

export function useModelFields(resource: string | undefined): FieldDef[] {
  const registry = useContext(ViewRegistryContext);
  return resource ? (registry?.models.get(resource)?.fields ?? EMPTY_FIELDS) : EMPTY_FIELDS;
}

export function readRecordValue(field: string, record: Row, fields: FieldDef[]): unknown {
  const readKey = fields.find((definition) => definition.name === field)?.read_key;
  return readKey && Object.hasOwn(record, readKey) ? record[readKey] : record[field];
}

// The read key holds full tag objects. A record whose read key holds none falls back to the
// write key's IDs, so a handler that returns only IDs shows them rather than an empty field.
export function readTagsValue(field: string, record: Row, fields: FieldDef[]): unknown[] {
  const value = readRecordValue(field, record, fields);
  if (Array.isArray(value) && value.some((tag) => typeof tag === "object" && tag !== null)) return value;
  const ids = record[field];
  return Array.isArray(ids) && ids.length > 0 ? ids : Array.isArray(value) ? value : [];
}

export function recordValueText(value: unknown): string {
  if (value == null) return "";
  if (Array.isArray(value)) return value.map(recordValueText).join(", ");
  if (typeof value === "object") {
    const row = value as Row;
    return String(row.display_name ?? row.name ?? row.id ?? "");
  }
  return String(value);
}

export function unresolvedRelationIds(field: string, rows: Row[], fields: FieldDef[]): string[] {
  return rows.flatMap((row) => {
    const value = readRecordValue(field, row, fields);
    if (typeof value === "string") return [value];
    if (
      typeof value === "object" &&
      value !== null &&
      "id" in value &&
      typeof value.id === "string" &&
      !("display_name" in value) &&
      !("name" in value) &&
      !("display" in value)
    ) {
      return [value.id];
    }
    return [];
  });
}
