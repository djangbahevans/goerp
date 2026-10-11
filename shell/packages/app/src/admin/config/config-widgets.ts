import { type ConfigEntry, MASKED } from "./config-api.js";

const STRING_WIDGETS = [
  "email",
  "url",
  "phone",
  "textarea",
  "country_select",
  "language_select",
  "timezone_select",
  "currency_select",
  "color_picker",
  "icon_picker",
] as const;

export type Widget =
  | (typeof STRING_WIDGETS)[number]
  | "checkbox"
  | "radio"
  | "unsupported"
  | "generated"
  | "toggle"
  | "select"
  | "multiselect"
  | "list"
  | "number"
  | "json"
  | "password"
  | "text";

export type Draft = string | boolean | string[];

const ARRAY_TYPES = new Set(["string[]", "integer[]", "float[]"]);

export function isArrayType(type: string): boolean {
  return ARRAY_TYPES.has(type);
}

export function widgetOf(entry: ConfigEntry): Widget {
  if (entry.generated) return "generated";
  if (entry.encrypted) return "password";
  if (entry.fieldType !== "") {
    const { fieldType, type } = entry;
    const stringWidget = STRING_WIDGETS.find((widget) => widget === fieldType);
    if (type === "string" && stringWidget) return stringWidget;
    switch (fieldType) {
      case "text":
        return type === "string" || type === "duration" ? "text" : "unsupported";
      case "password":
        return type === "string" ? "password" : "unsupported";
      case "duration":
        return type === "duration" ? "text" : "unsupported";
      case "number":
        return type === "integer" || type === "float" ? "number" : "unsupported";
      case "integer":
        return type === "integer" ? "number" : "unsupported";
      case "boolean":
        return type === "boolean" ? "checkbox" : "unsupported";
      case "toggle":
        return type === "boolean" ? "toggle" : "unsupported";
      case "json":
        return type === "json" ? "json" : "unsupported";
      case "select":
      case "radio":
        return ["string", "integer", "float"].includes(type) && entry.options.length > 0 ? fieldType : "unsupported";
      case "multi_select":
      case "multiselect":
        return isArrayType(type) && entry.options.length > 0 ? "multiselect" : "unsupported";
      default:
        return "unsupported";
    }
  }
  if (entry.type === "boolean") return "toggle";
  if (entry.options.length > 0) return isArrayType(entry.type) ? "multiselect" : "select";
  if (isArrayType(entry.type)) return "list";
  if (entry.type === "integer" || entry.type === "float") return "number";
  if (entry.type === "json") return "json";
  return "text";
}

export function draftOf(entry: ConfigEntry): Draft {
  const { value } = entry;
  switch (widgetOf(entry)) {
    case "toggle":
    case "checkbox":
      return value === true;
    case "multiselect":
    case "list":
      return Array.isArray(value) ? value.map(String) : [];
    case "password":
      return entry.encrypted ? (entry.isSet ? MASKED : "") : value == null ? "" : String(value);
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
  if (trimmed === "" || !Number.isFinite(Number(trimmed))) return { ok: false, message: "Enter a number." };
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

export function parseDraft(entry: ConfigEntry, draft: Draft): Parsed {
  const widget = widgetOf(entry);
  if (widget === "unsupported") return { ok: false, message: unsupportedMessage(entry) };
  if (entry.encrypted && draft === MASKED) return { ok: true, value: MASKED };
  const empty = Array.isArray(draft) ? draft.length === 0 : draft === "" || draft === false;
  if (widget !== "toggle" && widget !== "checkbox" && empty) {
    return entry.required && entry.default === null
      ? { ok: false, message: "This is required." }
      : { ok: true, value: null };
  }

  switch (entry.type) {
    case "boolean":
      if (entry.encrypted) {
        if (draft !== "true" && draft !== "false") return { ok: false, message: "Enter true or false." };
        return { ok: true, value: draft === "true" };
      }
      return { ok: true, value: draft === true };
    case "integer":
    case "float":
      return parseNumber(draft as string, entry.type === "integer");
    case "string[]":
    case "integer[]":
    case "float[]":
      if (entry.encrypted) {
        try {
          const items: unknown = JSON.parse(draft as string);
          if (
            !Array.isArray(items) ||
            items.some((item) => (entry.type === "string[]" ? typeof item !== "string" : typeof item !== "number"))
          )
            return { ok: false, message: "Enter a JSON array matching the setting's type." };
          return parseItems(entry, items.map(String));
        } catch {
          return { ok: false, message: "Enter a valid JSON array." };
        }
      }
      return parseItems(entry, draft as string[]);
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

export function unsupportedMessage(entry: ConfigEntry): string {
  return `Unsupported settings field type “${entry.fieldType}” for ${entry.type}. Check the module's configuration declaration.`;
}

export function splitList(text: string): string[] {
  return text
    .split(",")
    .map((item) => item.trim())
    .filter((item) => item !== "");
}

export function groupByCategory(entries: ConfigEntry[]): { category: string; entries: ConfigEntry[] }[] {
  const groups = new Map<string, ConfigEntry[]>();
  for (const entry of entries) {
    const category = entry.category || "Settings";
    groups.set(category, [...(groups.get(category) ?? []), entry]);
  }
  return [...groups].map(([category, grouped]) => ({ category, entries: grouped }));
}
