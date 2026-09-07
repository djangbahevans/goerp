import type { ReactNode } from "react";
import { Badge, type BadgeColor } from "./badge.js";
import { CountryFlag } from "./country-flag.js";
import { UserAvatar } from "./user-avatar.js";

// typescript-sdk-reference.md §13 "Form field components" — read-only
// display counterpart to the generic form/list renderers' field types
// (form-view-types.ts's FieldType), for custom views that render a value
// without going through the manifest-driven renderer. badge/avatar/country
// delegate to their already-styled sibling components (goerp#679); the
// remaining unimplemented display types (tags/relation/file/color/json/
// custom) are tracked separately (goerp#683).
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
  | "badge"
  | "avatar"
  | "country";

// Types formatFieldValue itself turns into a plain string — badge/avatar/
// country render as real elements instead (see Field below).
type FormattableFieldType = Exclude<FieldType, "badge" | "avatar" | "country">;

// Same shape as the list renderer's own `badge_config` (BadgeConfig in
// list-view-types.ts) — keyed by the field's raw value.
export type FieldBadgeConfig = Record<string, { label: string; color?: BadgeColor }>;

export interface FieldAvatarValue {
  userId?: string | undefined;
  name: string;
  avatarUrl?: string | null | undefined;
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
  } else {
    content = formatFieldValue(value, type, currency, emptyText);
  }

  const isMono = type === "currency" || type === "number";

  return (
    <span className="flex flex-col gap-1">
      {label !== undefined && <span className="text-sm text-text-secondary">{label}</span>}
      {href !== undefined ? (
        <a href={href} className="text-base text-primary hover:underline">
          {content}
        </a>
      ) : (
        <span className={`text-base text-text ${isMono ? "font-mono" : ""}`}>{content}</span>
      )}
    </span>
  );
}
