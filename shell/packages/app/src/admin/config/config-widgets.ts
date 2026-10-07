import { type ConfigEntry, MASKED } from "./config-api.js";

// shell-ux.md §5.4 "Config field rendering, by field_type": `type` is the
// value's data type and `field_type` the widget.
export type Widget =
  | "generated"
  | "toggle"
  | "select"
  | "multiselect"
  | "list"
  | "number"
  | "json"
  | "password"
  | "textarea"
  | "text";

export type Draft = string | boolean | string[];

const ARRAY_TYPES = new Set(["string[]", "integer[]", "float[]"]);

export function isArrayType(type: string): boolean {
  return ARRAY_TYPES.has(type);
}

export function widgetOf(entry: ConfigEntry): Widget {
  if (entry.generated) return "generated";
  if (entry.type === "boolean") return "toggle";
  if (entry.options.length > 0) return isArrayType(entry.type) ? "multiselect" : "select";
  if (isArrayType(entry.type)) return "list";
  if (entry.type === "integer" || entry.type === "float") return "number";
  if (entry.type === "json") return "json";
  if (entry.type === "string" && entry.encrypted) return "password";
  if (entry.fieldType === "textarea") return "textarea";
  return "text";
}

// The form's starting value for `entry`. A set encrypted value starts as the
// mask the server sent, never plaintext.
export function draftOf(entry: ConfigEntry): Draft {
  const { value } = entry;
  switch (widgetOf(entry)) {
    case "toggle":
      return value === true;
    case "multiselect":
    case "list":
      return Array.isArray(value) ? value.map(String) : [];
    case "password":
      return entry.isSet ? MASKED : "";
    case "json":
      return value === null || value === undefined ? "" : JSON.stringify(value, null, 2);
    case "generated":
      return typeof value === "string" ? value : "";
    default:
      return value === null || value === undefined ? "" : String(value);
  }
}

export function sameDraft(a: Draft, b: Draft): boolean {
  if (Array.isArray(a) && Array.isArray(b)) return a.length === b.length && a.every((item, i) => item === b[i]);
  return a === b;
}

export type Parsed = { ok: true; value: unknown } | { ok: false; message: string };

function parseNumber(text: string, whole: boolean): Parsed {
  const trimmed = text.trim();
  if (trimmed === "" || Number.isNaN(Number(trimmed))) return { ok: false, message: "Enter a number." };
  const n = Number(trimmed);
  if (whole && !Number.isInteger(n)) return { ok: false, message: "Enter a whole number." };
  return { ok: true, value: n };
}

function parseItems(entry: ConfigEntry, items: string[]): Parsed {
  if (entry.type === "string[]") return { ok: true, value: items };
  const numbers: number[] = [];
  for (const item of items) {
    const parsed = parseNumber(item, entry.type === "integer[]");
    if (!parsed.ok)
      return { ok: false, message: `"${item}" isn't ${entry.type === "integer[]" ? "a whole number" : "a number"}.` };
    numbers.push(parsed.value as number);
  }
  return { ok: true, value: numbers };
}

// The wire value for an edited draft. An emptied optional field resets to its
// default (null); an emptied required one is rejected here, before the server
// would.
export function parseDraft(entry: ConfigEntry, draft: Draft): Parsed {
  const widget = widgetOf(entry);
  const empty = Array.isArray(draft) ? draft.length === 0 : draft === "" || draft === false;
  if (widget !== "toggle" && empty) {
    return entry.required && entry.default === null
      ? { ok: false, message: "This is required." }
      : { ok: true, value: null };
  }

  switch (widget) {
    case "toggle":
      return { ok: true, value: draft === true };
    case "multiselect":
    case "list":
      return parseItems(entry, draft as string[]);
    case "number":
      return parseNumber(draft as string, entry.type === "integer");
    case "select":
      return entry.type === "integer" || entry.type === "float"
        ? parseNumber(draft as string, entry.type === "integer")
        : { ok: true, value: draft };
    case "json":
      try {
        return { ok: true, value: JSON.parse(draft as string) };
      } catch {
        return { ok: false, message: "Enter valid JSON." };
      }
    default:
      return { ok: true, value: draft };
  }
}

// Comma-separated list input, for array values with no fixed options.
export function splitList(text: string): string[] {
  return text
    .split(",")
    .map((item) => item.trim())
    .filter((item) => item !== "");
}

// The entries' category headings in first-appearance order.
export function groupByCategory(entries: ConfigEntry[]): { category: string; entries: ConfigEntry[] }[] {
  const groups = new Map<string, ConfigEntry[]>();
  for (const entry of entries) {
    const category = entry.category || "Settings";
    groups.set(category, [...(groups.get(category) ?? []), entry]);
  }
  return [...groups].map(([category, grouped]) => ({ category, entries: grouped }));
}
