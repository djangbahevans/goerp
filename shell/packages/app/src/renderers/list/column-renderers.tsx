import { Badge, type BadgeColor, CountryFlag, UserAvatar } from "@goerp/sdk/components";
import type { CSSProperties, ReactNode } from "react";
import type { ListColumn, Row } from "./list-view-types.js";

// manifest-spec.md's `href`/`format` template vars: `{record.id}`, `{record.field_name}`.
export function renderHref(template: string, row: Row): string {
  return template.replace(/\{record\.([\w.]+)\}/g, (_match, field: string) => String(row[field] ?? ""));
}

// manifest-spec.md §9.1's width/align/truncate ListColumn fields —
// `truncate` defaults to true, `align` to "left" ("right" for numbers is
// left to the manifest author to declare explicitly; this doesn't infer
// it from `type`).
export function columnStyle(column: ListColumn): CSSProperties {
  const style: CSSProperties = {};
  if (column.width !== undefined) style.width = column.width;
  if (column.min_width !== undefined) style.minWidth = column.min_width;
  if (column.max_width !== undefined) style.maxWidth = column.max_width;
  if (column.align) style.textAlign = column.align;
  if (column.truncate !== false) {
    style.overflow = "hidden";
    style.textOverflow = "ellipsis";
    style.whiteSpace = "nowrap";
  }
  return style;
}

function toDate(value: unknown): Date | null {
  if (value instanceof Date) return value;
  if (typeof value === "string" || typeof value === "number") {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? null : date;
  }
  return null;
}

// A pragmatic subset of CLDR date-pattern tokens — not a full CLDR
// implementation, but covers manifest-spec.md's own example ("dd MMM yyyy")
// and the common year/month/day/hour/minute/second cases.
const CLDR_TOKEN_ORDER = ["yyyy", "yy", "MMMM", "MMM", "MM", "dd", "d", "HH", "H", "hh", "h", "mm", "ss", "a"];
const CLDR_TOKEN_PATTERN = new RegExp(CLDR_TOKEN_ORDER.join("|"), "g");

function formatCLDR(date: Date, pattern: string): string {
  return pattern.replace(CLDR_TOKEN_PATTERN, (token) => {
    switch (token) {
      case "yyyy":
        return String(date.getFullYear());
      case "yy":
        return String(date.getFullYear()).slice(-2);
      case "MMMM":
        return date.toLocaleString(undefined, { month: "long" });
      case "MMM":
        return date.toLocaleString(undefined, { month: "short" });
      case "MM":
        return String(date.getMonth() + 1).padStart(2, "0");
      case "dd":
        return String(date.getDate()).padStart(2, "0");
      case "d":
        return String(date.getDate());
      case "HH":
        return String(date.getHours()).padStart(2, "0");
      case "H":
        return String(date.getHours());
      case "hh":
        return String(date.getHours() % 12 || 12).padStart(2, "0");
      case "h":
        return String(date.getHours() % 12 || 12);
      case "mm":
        return String(date.getMinutes()).padStart(2, "0");
      case "ss":
        return String(date.getSeconds()).padStart(2, "0");
      case "a":
        return date.getHours() < 12 ? "AM" : "PM";
      default:
        return token;
    }
  });
}

function formatDate(value: unknown, format: string | undefined, options: Intl.DateTimeFormatOptions): string {
  const date = toDate(value);
  if (!date) return "";
  if (format) return formatCLDR(date, format);
  return new Intl.DateTimeFormat(undefined, options).format(date);
}

const RELATIVE_DIVISIONS: [number, Intl.RelativeTimeFormatUnit][] = [
  [60, "seconds"],
  [60, "minutes"],
  [24, "hours"],
  [7, "days"],
  [4.34524, "weeks"],
  [12, "months"],
  [Number.POSITIVE_INFINITY, "years"],
];

function formatRelativeTime(value: unknown): string {
  const date = toDate(value);
  if (!date) return "";
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  let duration = (date.getTime() - Date.now()) / 1000;
  for (const [amount, unit] of RELATIVE_DIVISIONS) {
    if (Math.abs(duration) < amount) return rtf.format(Math.round(duration), unit);
    duration /= amount;
  }
  return rtf.format(Math.round(duration), "years");
}

function Pill({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${className ?? "bg-gray-100 text-gray-800"}`}
    >
      {children}
    </span>
  );
}

interface FileFieldValue {
  url: string;
  id?: string;
  name?: string;
}

// object-storage-guide.md §5/§6: a model.File()/FileMulti() field's derived
// read key already carries a server-generated signed/public URL in the
// response — the shell never constructs file URLs itself.
function isFileFieldValue(value: unknown): value is FileFieldValue {
  return (
    typeof value === "object" && value !== null && "url" in value && typeof (value as FileFieldValue).url === "string"
  );
}

function fileUrlOf(value: unknown): string | null {
  if (isFileFieldValue(value)) return value.url;
  if (typeof value === "string" && value) return value;
  return null;
}

export interface RenderCellOptions {
  // Resolved separately, since it needs an async batch-fetch when
  // `display_field` isn't set — see use-relation-labels.ts.
  relationLabel?: string;
}

export function renderCellContent(column: ListColumn, row: Row, options: RenderCellOptions = {}): ReactNode {
  const value = row[column.field];
  const type = column.type ?? "text";

  switch (type) {
    case "text":
      return value == null ? "" : String(value);

    case "number":
      return typeof value === "number" ? new Intl.NumberFormat(undefined).format(value) : "";

    case "currency": {
      if (typeof value !== "number") return "";
      const currency = column.currency_field ? (row[column.currency_field] as string | undefined) : undefined;
      if (currency) {
        try {
          return new Intl.NumberFormat(undefined, { style: "currency", currency }).format(value);
        } catch {
          // currency_field held something that isn't a valid ISO 4217 code — fall through to plain formatting.
        }
      }
      return new Intl.NumberFormat(undefined).format(value);
    }

    case "percent":
      return typeof value === "number" ? new Intl.NumberFormat(undefined, { style: "percent" }).format(value) : "";

    case "date":
      return formatDate(value, column.format, { dateStyle: "medium" });

    case "datetime":
      return formatDate(value, column.format, { dateStyle: "medium", timeStyle: "short" });

    case "time":
      return formatDate(value, column.format, { timeStyle: "short" });

    case "relative_time":
      return formatRelativeTime(value);

    case "boolean":
      return value ? (
        <span role="img" aria-label="Yes">
          ✓
        </span>
      ) : (
        <span role="img" aria-label="No">
          ✗
        </span>
      );

    case "badge": {
      if (typeof value !== "string" && typeof value !== "number") return "";
      const key = String(value);
      const badge = column.badge_config?.[key];
      if (!badge) return key;
      // badge.color is a loosely-typed wire string(enum), not statically
      // checked — Badge itself falls back to gray for an unrecognized one.
      return <Badge label={badge.label} color={badge.color as BadgeColor | undefined} icon={badge.icon} />;
    }

    case "avatar": {
      const avatarValue = column.avatar_field ? row[column.avatar_field] : value;
      const url = fileUrlOf(avatarValue);
      const label = typeof value === "string" ? value : "";
      return <UserAvatar name={label} avatarUrl={url} size="md" />;
    }

    case "email":
      return typeof value === "string" && value ? <a href={`mailto:${value}`}>{value}</a> : "";

    case "phone":
      return typeof value === "string" && value ? <a href={`tel:${value}`}>{value}</a> : "";

    case "url":
      return typeof value === "string" && value ? (
        <a href={value} target="_blank" rel="noreferrer">
          {value}
        </a>
      ) : (
        ""
      );

    case "country":
      return typeof value === "string" && value ? <CountryFlag code={value} showName /> : "";

    case "tags":
      return Array.isArray(value) ? (
        <span className="inline-flex flex-wrap gap-1">
          {value.map((tag) => (
            <Pill key={String(tag)}>{String(tag)}</Pill>
          ))}
        </span>
      ) : (
        ""
      );

    case "relation": {
      const display = column.display_field ? row[column.display_field] : options.relationLabel;
      return display == null ? "" : String(display);
    }

    case "file": {
      const url = fileUrlOf(value);
      const name = isFileFieldValue(value) ? (value.name ?? value.id) : typeof value === "string" ? value : "";
      if (!url) return name;
      return (
        <a href={url} target="_blank" rel="noreferrer">
          {name || "Download"}
        </a>
      );
    }

    case "color":
      return typeof value === "string" && value ? (
        <span
          role="img"
          aria-label={value}
          className="inline-block h-4 w-4 rounded border border-gray-300"
          style={{ backgroundColor: value }}
        />
      ) : (
        ""
      );

    case "json":
      return value == null ? (
        ""
      ) : (
        <details>
          <summary>View</summary>
          <pre className="whitespace-pre-wrap text-xs">{JSON.stringify(value, null, 2)}</pre>
        </details>
      );

    case "custom":
      // Requires a module component registry (defineModule's fieldRenderers.{})
      // that doesn't exist in this repo yet — renders the raw value as a
      // fallback rather than silently nothing.
      return value == null ? "" : String(value);

    default:
      return value == null ? "" : String(value);
  }
}

export function renderCell(column: ListColumn, row: Row, options: RenderCellOptions = {}): ReactNode {
  const content = renderCellContent(column, row, options);
  if (column.href) {
    const href = renderHref(column.href, row);
    return <a href={href}>{content}</a>;
  }
  return content;
}
