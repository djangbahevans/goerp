import type { ReactNode } from "react";

// typescript-sdk-reference.md §13 "Form field components" — read-only
// display counterpart to the generic form/list renderers' field types
// (form-view-types.ts's FieldType), for custom views that render a value
// without going through the manifest-driven renderer.
export type FieldType =
  | "text"
  | "email"
  | "phone"
  | "url"
  | "number"
  | "currency"
  | "percent"
  | "boolean"
  | "date"
  | "datetime"
  | "time";

export interface FieldProps {
  label?: string | undefined;
  value: unknown;
  type?: FieldType | undefined;
  // Static code, or a value already resolved from another field
  // (manifest `currency_field`) — this component only ever sees the
  // resolved string.
  currency?: string | undefined;
  emptyText?: string | undefined;
  // Renders the formatted value as a link (documented on the "Manager"
  // field in EmploymentTab).
  href?: string | undefined;
}

function formatDateLike(value: unknown, options: Intl.DateTimeFormatOptions, emptyText: string): string {
  const date = value instanceof Date ? value : new Date(String(value));
  if (Number.isNaN(date.getTime())) return emptyText;
  return new Intl.DateTimeFormat(undefined, options).format(date);
}

// l10n-guide.md: "All monetary amounts are stored and transmitted as
// integer minor units." (pesewas, cents, ...) — the ISO 4217 decimal-place
// count varies per currency (XOF 0, USD 2, KWD 3), which Intl already
// resolves internally; reading it back out of resolvedOptions() avoids
// hardcoding a minor-unit table here.
export function currencyMinorUnitDigits(currency: string): number {
  return new Intl.NumberFormat(undefined, { style: "currency", currency }).resolvedOptions().maximumFractionDigits ?? 2;
}

export function formatFieldValue(
  value: unknown,
  type: FieldType,
  currency: string | undefined,
  emptyText: string,
): string {
  if (value === null || value === undefined || value === "") return emptyText;

  switch (type) {
    case "currency": {
      const n = typeof value === "number" ? value : Number(value);
      if (Number.isNaN(n)) return emptyText;
      if (!currency) return new Intl.NumberFormat().format(n);
      const digits = currencyMinorUnitDigits(currency);
      return new Intl.NumberFormat(undefined, { style: "currency", currency }).format(n / 10 ** digits);
    }
    case "percent": {
      const n = typeof value === "number" ? value : Number(value);
      if (Number.isNaN(n)) return emptyText;
      return new Intl.NumberFormat(undefined, { style: "percent", maximumFractionDigits: 2 }).format(n);
    }
    case "number": {
      const n = typeof value === "number" ? value : Number(value);
      return Number.isNaN(n) ? String(value) : new Intl.NumberFormat().format(n);
    }
    case "date":
      return formatDateLike(value, { dateStyle: "medium" }, emptyText);
    case "datetime":
      return formatDateLike(value, { dateStyle: "medium", timeStyle: "short" }, emptyText);
    case "time":
      return formatDateLike(value, { timeStyle: "short" }, emptyText);
    case "boolean":
      return value === true ? "Yes" : value === false ? "No" : emptyText;
    default:
      return String(value);
  }
}

export function Field({ label, value, type = "text", currency, emptyText = "—", href }: FieldProps): ReactNode {
  const formatted = formatFieldValue(value, type, currency, emptyText);
  return (
    <span>
      {label !== undefined && <span>{label}: </span>}
      {href !== undefined ? <a href={href}>{formatted}</a> : <span>{formatted}</span>}
    </span>
  );
}
