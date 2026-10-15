import { datePreferencesStore } from "../i18n/date-format.js";

const PLACEHOLDER = /\{([^}]+)\}/g;
const RECORD_PREFIX = "record.";

type Lookup = { found: true; value: unknown } | { found: false };

function lookupRecordField(record: Record<string, unknown>, key: string): Lookup {
  let value: unknown = record;
  for (const segment of key.slice(RECORD_PREFIX.length).split(".")) {
    if (value === null || typeof value !== "object") return { found: false };
    value = (value as Record<string, unknown>)[segment];
  }
  return { found: true, value };
}

// The en-CA locale formats a date as YYYY-MM-DD. An unrecognised timezone falls
// back to UTC, since interpolation never throws.
function today(): string {
  const now = new Date();
  const format = (timeZone: string) =>
    new Intl.DateTimeFormat("en-CA", { timeZone, year: "numeric", month: "2-digit", day: "2-digit" }).format(now);
  try {
    return format(datePreferencesStore.get().timezone);
  } catch {
    return format("UTC");
  }
}

// Substitutes {record.field}, {record.nested.field} and {today}. A missing
// record or field resolves to "", and an unrecognised placeholder is left as is.
export function interpolate(template: string, record: Record<string, unknown> | null): string {
  return template.replace(PLACEHOLDER, (match, key: string) => {
    if (key === "today") return today();
    if (!key.startsWith(RECORD_PREFIX)) return match;
    if (!record) return "";
    const lookup = lookupRecordField(record, key);
    return lookup.found && lookup.value != null ? String(lookup.value) : "";
  });
}

// Substitutes {record.field} placeholders in a path. A placeholder that resolves
// to nothing is dropped with a warning, along with the slash it leaves behind,
// so "/sales/invoices/{record.id}" degrades to "/sales/invoices".
export function interpolatePath(template: string, record: Record<string, unknown> | null): string {
  const result = template.replace(PLACEHOLDER, (match, key: string) => {
    if (!key.startsWith(RECORD_PREFIX)) return match;
    if (!record) {
      console.warn(`interpolatePath: no record in context for placeholder ${match}`);
      return "";
    }
    const lookup = lookupRecordField(record, key);
    if (!lookup.found) {
      console.warn(`interpolatePath: missing key ${key} in record`);
      return "";
    }
    if (lookup.value == null || String(lookup.value) === "") {
      console.warn(`interpolatePath: key ${key} resolved to empty`);
      return "";
    }
    return String(lookup.value);
  });
  return result.replace(/\/\/+/g, "/").replace(/(.)\/$/, "$1");
}
