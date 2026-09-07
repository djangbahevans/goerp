import type { ReactNode } from "react";
import { Badge, type BadgeColor } from "./badge.js";
import { CountryFlag } from "./country-flag.js";
import { UserAvatar } from "./user-avatar.js";

// typescript-sdk-reference.md §13 "Form field components" — read-only
// display counterpart to the generic form/list renderers' field types
// (form-view-types.ts's FieldType), for custom views that render a value
// without going through the manifest-driven renderer. badge/avatar/country
// delegate to their already-styled sibling components (goerp#679).
// `custom` is deferred until the custom-field-renderer-registration
// mechanism (a separate, currently unbuilt backlog item) exists.
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
  | "time"
  | "relative_time"
  | "badge"
  | "avatar"
  | "country"
  | "tags"
  | "relation"
  | "file"
  | "color"
  | "json"
  | "custom";

// Types formatFieldValue itself turns into a plain string — badge/avatar/
// country/color render as real elements instead (see Field below).
type FormattableFieldType = Exclude<FieldType, "badge" | "avatar" | "country" | "color">;

// Same shape as the list renderer's own `badge_config` (BadgeConfig in
// list-view-types.ts) — keyed by the field's raw value.
export type FieldBadgeConfig = Record<string, { label: string; color?: BadgeColor }>;

export interface FieldAvatarValue {
  userId?: string | undefined;
  name: string;
  avatarUrl?: string | null | undefined;
}

export interface FieldTagValue {
  name: string;
}

export interface FieldRelationValue {
  display: string;
}

// Renders as plain text only (field.md's own "file" row: "the value's exact
// shape is still unspecified — flagged for a follow-up"); a caller wanting
// a clickable filename passes Field's own top-level `href`, the same
// caller-supplied pattern the "Manager" field example already uses.
export interface FieldFileValue {
  id?: string | undefined;
  name?: string | undefined;
}

const RELATIVE_TIME_DIVISIONS: [number, Intl.RelativeTimeFormatUnit][] = [
  [60, "seconds"],
  [60, "minutes"],
  [24, "hours"],
  [7, "days"],
  [4.34524, "weeks"],
  [12, "months"],
  [Number.POSITIVE_INFINITY, "years"],
];

// Shared with column-renderers.tsx's "relative_time" list column type, so
// the manifest-declared and hand-rendered versions of the same field agree
// on the exact same division/rounding behavior.
export function formatRelativeTime(value: unknown, emptyText: string): string {
  const date = coerceDate(value);
  if (!date) return emptyText;
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  let duration = (date.getTime() - Date.now()) / 1000;
  for (const [amount, unit] of RELATIVE_TIME_DIVISIONS) {
    if (Math.abs(duration) < amount) return rtf.format(Math.round(duration), unit);
    duration /= amount;
  }
  return rtf.format(Math.round(duration), "years");
}

export interface FieldProps {
  label?: string | undefined;
  value: unknown;
  type?: FieldType | undefined;
  // Static code, or a value already resolved from another field
  // (manifest `currency_field`) — this component only ever sees the
  // resolved string.
  currency?: string | undefined;
  // Required when type="badge".
  badgeConfig?: FieldBadgeConfig | undefined;
  emptyText?: string | undefined;
  // Renders the formatted value as a link (documented on the "Manager"
  // field in EmploymentTab).
  href?: string | undefined;
}

// Passes a number/string straight to the Date constructor rather than
// stringifying first — new Date(String(1699999999999)) is Invalid Date,
// since a bare numeric string isn't accepted as an epoch-ms timestamp the
// way the raw number is.
function coerceDate(value: unknown): Date | undefined {
  if (value instanceof Date) return value;
  if (typeof value === "string" || typeof value === "number") {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? undefined : date;
  }
  return undefined;
}

function formatDateLike(value: unknown, options: Intl.DateTimeFormatOptions, emptyText: string): string {
  const date = coerceDate(value);
  if (!date) return emptyText;
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
  type: FormattableFieldType,
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
    case "relative_time":
      return formatRelativeTime(value, emptyText);
    case "tags": {
      if (!Array.isArray(value)) return emptyText;
      const names = value
        .map((tag: FieldTagValue) => tag?.name)
        .filter((name): name is string => typeof name === "string");
      return names.length > 0 ? names.join(", ") : emptyText;
    }
    case "relation": {
      const relation = value as FieldRelationValue;
      return typeof relation.display === "string" ? relation.display : emptyText;
    }
    case "file": {
      const file = value as FieldFileValue | string;
      if (typeof file === "string") return file;
      return file.name ?? file.id ?? emptyText;
    }
    case "json":
      try {
        return JSON.stringify(value) ?? emptyText;
      } catch {
        // A circular reference or a BigInt value — JSON.stringify throws
        // synchronously rather than returning undefined for those.
        return emptyText;
      }
    default:
      return String(value);
  }
}

export function Field({
  label,
  value,
  type = "text",
  currency,
  badgeConfig,
  emptyText = "—",
  href,
}: FieldProps): ReactNode {
  let content: ReactNode;

  if (type === "badge") {
    const key = value === null || value === undefined || value === "" ? undefined : String(value);
    const config = key !== undefined ? badgeConfig?.[key] : undefined;
    content = config ? <Badge label={config.label} color={config.color} /> : (key ?? emptyText);
  } else if (type === "avatar") {
    const avatar =
      typeof value === "object" && value !== null && "name" in value && typeof value.name === "string"
        ? (value as FieldAvatarValue)
        : undefined;
    content = avatar ? (
      <UserAvatar userId={avatar.userId} name={avatar.name} avatarUrl={avatar.avatarUrl} size="sm" />
    ) : (
      emptyText
    );
  } else if (type === "country") {
    content = typeof value === "string" && value !== "" ? <CountryFlag code={value} showName /> : emptyText;
  } else if (type === "color") {
    content =
      typeof value === "string" && value !== "" ? (
        <span className="inline-flex items-center gap-1">
          {/* The swatch's own outline always renders, regardless of the
              value's fill — an uncontrolled arbitrary color could otherwise
              exactly match the surrounding background and disappear. */}
          <span
            aria-hidden="true"
            className="inline-block h-3.5 w-3.5 rounded-full border border-border"
            style={{ backgroundColor: value }}
          />
          {value}
        </span>
      ) : (
        emptyText
      );
  } else {
    content = formatFieldValue(value, type, currency, emptyText);
  }

  const isMono = type === "currency" || type === "number" || type === "json";
  const jsonTitle = type === "json" && typeof content === "string" ? content : undefined;
  const valueClassName = `text-base ${isMono ? "font-mono" : ""} ${type === "json" ? "block truncate" : ""}`;

  return (
    <span className="flex flex-col gap-1">
      {label !== undefined && <span className="text-sm text-text-secondary">{label}</span>}
      {href !== undefined ? (
        <a href={href} title={jsonTitle} className={`${valueClassName} text-primary hover:underline`}>
          {content}
        </a>
      ) : (
        <span title={jsonTitle} className={`${valueClassName} text-text`}>
          {content}
        </span>
      )}
    </span>
  );
}
