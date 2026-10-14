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
    return typeof value === "string" ? [value] : [];
  });
}
