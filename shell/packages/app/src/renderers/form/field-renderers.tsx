import { apiClient } from "@goerp/sdk";
import type { TagValue } from "@goerp/sdk/components";
import {
  CodeField,
  ColorPicker,
  DateField,
  DateTimeField,
  MoneyField,
  RichTextField,
  SignaturePad,
  TagsField,
  TimeField,
} from "@goerp/sdk/components";
import { createInfiniteListQueryOptions } from "@goerp/sdk/react";
import { resourceRegistry } from "@goerp/sdk/schema";
import { useInfiniteQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { Row } from "../list/list-view-types.js";
import type { FieldOption, FormField } from "./form-view-types.js";

export type { TagValue } from "@goerp/sdk/components";

// "tags" writes `{field}` (an `_ids` key) as a UUID array but reads the
// plural key with `_ids` stripped ("tags"), holding full {id,name,color}.
function tagsReadKey(field: string): string {
  return field.replace(/_ids$/, "s");
}

function isTagValue(value: unknown): value is TagValue {
  return typeof value === "object" && value !== null && "id" in value && "name" in value;
}

export function readFieldValue(field: FormField, record: Row): unknown {
  if (field.type === "tags") return record[tagsReadKey(field.field)];
  return record[field.field];
}

// "date_range"/"address" bind to other named fields, so their onChange
// patch is merged as-is instead of wrapped under this field's own name.
export function writeFieldValue(field: FormField, value: unknown): Record<string, unknown> {
  if (field.type === "date_range" || field.type === "address") return value as Record<string, unknown>;
  return { [field.field]: value };
}

function toNumber(raw: string): number | undefined {
  if (raw === "") return undefined;
  const n = Number(raw);
  return Number.isNaN(n) ? undefined : n;
}

// Shared by the "date"/"datetime" cases below — a record field's raw value
// (an ISO string from the API, or absent) parsed into the Date DateField/
// DateTimeField expect, or undefined for anything that isn't a valid date.
function parseFieldDate(value: unknown): Date | undefined {
  if (typeof value !== "string") return undefined;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? undefined : date;
}

// Needs resource+resource_label_field (backlog #674 covers the
// default-label-field case). Reuses useInfiniteList's query-options
// factory rather than a parallel fetch; just the first page.
function useResourceOptions(resource: string | undefined, labelField: string | undefined, enabled: boolean) {
  const query = useInfiniteQuery({
    ...createInfiniteListQueryOptions<Row>(resource ?? "", { limit: 100 }, resourceRegistry, apiClient),
    enabled: enabled && Boolean(resource) && Boolean(labelField),
  });
  return { data: query.data?.pages[0]?.data, isLoading: query.isLoading };
}

function ResourceSelect({
  field,
  id,
  value,
  onChange,
  disabled,
}: {
  field: FormField;
  id?: string | undefined;
  value: unknown;
  onChange: (value: unknown) => void;
  disabled: boolean;
}) {
  const labelField = field.resource_label_field;
  const { data: options, isLoading } = useResourceOptions(field.resource, labelField, true);

  if (!field.resource || !labelField) {
    // Deferred, same as list-renderer.tsx's relation-column fallback:
    // no default-label-field resolution from the registry yet (backlog #674).
    return <span>{value == null ? "" : String(value)}</span>;
  }

  if (field.multiple) {
    const selected = new Set(Array.isArray(value) ? value.map(String) : []);
    return (
      <select
        id={id}
        multiple
        disabled={disabled || isLoading}
        value={[...selected]}
        onChange={(event) => onChange([...event.target.selectedOptions].map((o) => o.value))}
      >
        {(options ?? []).map((row) => (
          <option key={String(row.id)} value={String(row.id)}>
            {String(row[labelField] ?? row.id)}
          </option>
        ))}
      </select>
    );
  }

  return (
    <select
      id={id}
      disabled={disabled || isLoading}
      value={typeof value === "string" ? value : ""}
      onChange={(event) => onChange(event.target.value || undefined)}
    >
      <option value="">—</option>
      {(options ?? []).map((row) => (
        <option key={String(row.id)} value={String(row.id)}>
          {String(row[labelField] ?? row.id)}
        </option>
      ))}
    </select>
  );
}

function TagsInput({
  field,
  id,
  value,
  onChange,
  disabled,
}: {
  field: FormField;
  id?: string | undefined;
  value: unknown;
  onChange: (value: unknown) => void;
  disabled: boolean;
}) {
  const labelField = field.resource_label_field ?? "name";
  const { data: rows } = useResourceOptions(field.resource, labelField, true);
  // `value` (the plural read key) never reflects a pending edit, since
  // writes land under the singular `_ids` key — local state is the
  // display source of truth until the record itself reloads (a fresh
  // fetch or a post-save server response), which does change `value`.
  const [selected, setSelected] = useState<TagValue[]>(() => (Array.isArray(value) ? value.filter(isTagValue) : []));
  useEffect(() => {
    setSelected(Array.isArray(value) ? value.filter(isTagValue) : []);
  }, [value]);

  if (!field.resource) {
    return <span>{selected.map((t) => t.name).join(", ")}</span>;
  }

  const options: TagValue[] = (rows ?? []).map((row) => ({
    id: String(row.id),
    name: String(row[labelField] ?? row.id),
  }));

  const commit = (next: TagValue[]) => {
    setSelected(next);
    onChange(next.map((t) => t.id));
  };

  return (
    <TagsField
      id={id}
      value={selected}
      onChange={commit}
      options={options}
      disabled={disabled}
      placeholder={`Add ${field.label ?? field.field}…`}
    />
  );
}

function OptionsSelect({
  field,
  id,
  options,
  value,
  onChange,
  disabled,
}: {
  field: FormField;
  id?: string | undefined;
  options: FieldOption[];
  value: unknown;
  onChange: (value: unknown) => void;
  disabled: boolean;
}) {
  if (field.multiple) {
    const selected = new Set(Array.isArray(value) ? value.map(String) : []);
    return (
      <select
        id={id}
        multiple
        disabled={disabled}
        value={[...selected]}
        onChange={(event) => onChange([...event.target.selectedOptions].map((o) => o.value))}
      >
        {options.map((option) => (
          <option key={option.value} value={option.value} disabled={option.disabled}>
            {option.label}
          </option>
        ))}
      </select>
    );
  }
  return (
    <select
      id={id}
      disabled={disabled}
      value={typeof value === "string" ? value : ""}
      onChange={(event) => onChange(event.target.value || undefined)}
    >
      <option value="">—</option>
      {options.map((option) => (
        <option key={option.value} value={option.value} disabled={option.disabled}>
          {option.label}
        </option>
      ))}
    </select>
  );
}

// Intl.supportedValuesOf covers timezone/currency; no enumeration API
// exists for country/language, so those fall back to free-text input.
function supportedValuesOrEmpty(key: "currency" | "timeZone"): string[] {
  try {
    return Intl.supportedValuesOf(key);
  } catch {
    return [];
  }
}

export interface FieldInputProps {
  field: FormField;
  value: unknown;
  onChange: (value: unknown) => void;
  record: Row;
  disabled?: boolean;
  // FormFieldRow's label target (goerp#698) — omitted for types with no
  // single primary control (compound, self-labeled group/canvas, none).
  id?: string | undefined;
}

export function FieldInput({ field, value, onChange, record, disabled = false, id }: FieldInputProps) {
  const type = field.type ?? "text";
  const stringValue = typeof value === "string" ? value : value == null ? "" : String(value);

  switch (type) {
    case "text":
    case "email":
    case "phone":
    case "url":
      return (
        <input
          id={id}
          type={type === "text" ? "text" : type === "phone" ? "tel" : type}
          value={stringValue}
          placeholder={field.placeholder}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        />
      );

    case "textarea":
    case "markdown":
      // markdown falls back to a plain textarea — no editor library chosen
      // yet.
      return (
        <textarea
          id={id}
          value={stringValue}
          rows={field.rows ?? 3}
          placeholder={field.placeholder}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        />
      );

    case "rich_text":
      return (
        <RichTextField
          ariaLabelledBy={id}
          value={stringValue}
          rows={field.rows}
          disabled={disabled}
          onChange={onChange}
        />
      );

    case "code":
      return (
        <CodeField
          ariaLabelledBy={id}
          value={stringValue}
          language={field.language}
          rows={field.rows}
          disabled={disabled}
          onChange={onChange}
        />
      );

    case "number":
    case "integer":
      return (
        <input
          id={id}
          type="number"
          value={stringValue}
          min={field.min}
          max={field.max}
          step={type === "integer" ? 1 : field.step}
          disabled={disabled}
          onChange={(e) => onChange(toNumber(e.target.value))}
        />
      );

    case "currency": {
      const currency = field.currency_field ? (record[field.currency_field] as string | undefined) : undefined;
      return (
        <MoneyField
          id={id}
          value={typeof value === "number" ? value : undefined}
          currency={currency}
          min={field.min}
          max={field.max}
          disabled={disabled}
          onChange={onChange}
        />
      );
    }

    case "percent": {
      // "Stored as decimal 0.0-1.0 by convention" — the input shows/edits
      // whole percent points (0-100), converting on the way in/out.
      const percentValue = typeof value === "number" ? value * 100 : "";
      return (
        <input
          id={id}
          type="number"
          min={field.min ?? 0}
          max={field.max ?? 100}
          value={percentValue}
          disabled={disabled}
          onChange={(e) => {
            const n = toNumber(e.target.value);
            onChange(n === undefined ? undefined : n / 100);
          }}
        />
      );
    }

    case "date":
      return (
        <DateField
          id={id}
          value={parseFieldDate(value)}
          disabled={disabled}
          onChange={(date) => onChange(date?.toISOString().slice(0, 10))}
        />
      );
    case "datetime":
      return (
        <DateTimeField
          id={id}
          value={parseFieldDate(value)}
          disabled={disabled}
          onChange={(date) => onChange(date?.toISOString())}
        />
      );
    case "time":
      return (
        <TimeField
          id={id}
          value={typeof value === "string" ? value : undefined}
          disabled={disabled}
          onChange={onChange}
        />
      );

    case "date_range": {
      const startField = field.range_start_field ?? `${field.field}_start`;
      const endField = field.range_end_field ?? `${field.field}_end`;
      return (
        <span>
          <input
            type="date"
            aria-label={`${field.label ?? field.field} start`}
            value={typeof record[startField] === "string" ? (record[startField] as string) : ""}
            disabled={disabled}
            onChange={(e) => onChange({ [startField]: e.target.value })}
          />
          <input
            type="date"
            aria-label={`${field.label ?? field.field} end`}
            value={typeof record[endField] === "string" ? (record[endField] as string) : ""}
            disabled={disabled}
            onChange={(e) => onChange({ [endField]: e.target.value })}
          />
        </span>
      );
    }

    case "duration": {
      // Stored as integer minutes; edited as separate hours/minutes inputs.
      const totalMinutes = typeof value === "number" ? value : 0;
      const hours = Math.floor(totalMinutes / 60);
      const minutes = totalMinutes % 60;
      return (
        <span>
          <input
            type="number"
            min={0}
            aria-label={`${field.label ?? field.field} hours`}
            value={hours}
            disabled={disabled}
            onChange={(e) => onChange((toNumber(e.target.value) ?? 0) * 60 + minutes)}
          />
          <input
            type="number"
            min={0}
            max={59}
            aria-label={`${field.label ?? field.field} minutes`}
            value={minutes}
            disabled={disabled}
            onChange={(e) => onChange(hours * 60 + (toNumber(e.target.value) ?? 0))}
          />
        </span>
      );
    }

    case "boolean":
    case "toggle":
      return (
        <input
          id={id}
          type="checkbox"
          role={type === "toggle" ? "switch" : undefined}
          checked={value === true}
          disabled={disabled}
          onChange={(e) => onChange(e.target.checked)}
        />
      );

    case "select":
    case "multi_select":
      if (field.resource)
        return (
          <ResourceSelect
            field={{ ...field, multiple: field.multiple || type === "multi_select" }}
            id={id}
            value={value}
            onChange={onChange}
            disabled={disabled}
          />
        );
      return (
        <OptionsSelect
          field={{ ...field, multiple: field.multiple || type === "multi_select" }}
          id={id}
          options={field.options ?? []}
          value={value}
          onChange={onChange}
          disabled={disabled}
        />
      );

    case "radio":
      return (
        <span role="radiogroup" aria-label={field.label ?? field.field}>
          {(field.options ?? []).map((option) => (
            <label key={option.value}>
              <input
                type="radio"
                name={field.field}
                value={option.value}
                disabled={disabled || option.disabled}
                checked={stringValue === option.value}
                onChange={() => onChange(option.value)}
              />
              {option.label}
            </label>
          ))}
        </span>
      );

    case "relation":
    case "many2many":
      return (
        <ResourceSelect
          field={{ ...field, multiple: field.multiple || type === "many2many" }}
          id={id}
          value={value}
          onChange={onChange}
          disabled={disabled}
        />
      );

    case "tags":
      return <TagsInput field={field} id={id} value={value} onChange={onChange} disabled={disabled} />;

    case "user_select":
      return <ResourceSelect field={field} id={id} value={value} onChange={onChange} disabled={disabled} />;

    case "country_select":
    case "language_select":
      // No Intl enumeration API for regions/languages — free-text code
      // entry until a real dataset is wired in.
      return (
        <input
          id={id}
          type="text"
          value={stringValue}
          placeholder={type === "country_select" ? "ISO 3166-1 code" : "BCP 47 tag"}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        />
      );

    case "timezone_select":
    case "currency_select": {
      const values = supportedValuesOrEmpty(type === "timezone_select" ? "timeZone" : "currency");
      if (values.length === 0) {
        return (
          <input
            id={id}
            type="text"
            value={stringValue}
            disabled={disabled}
            onChange={(e) => onChange(e.target.value)}
          />
        );
      }
      return (
        <select id={id} disabled={disabled} value={stringValue} onChange={(e) => onChange(e.target.value)}>
          <option value="">—</option>
          {values.map((v) => (
            <option key={v} value={v}>
              {v}
            </option>
          ))}
        </select>
      );
    }

    case "color_picker":
      return <ColorPicker id={id} value={stringValue} disabled={disabled} onChange={onChange} />;

    case "icon_picker":
      // Free-text Lucide icon name — no icon-browsing picker UI yet.
      return (
        <input
          id={id}
          type="text"
          value={stringValue}
          placeholder="lucide icon name"
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        />
      );

    case "file":
    case "image":
    case "avatar_upload":
      // The presigned-upload flow isn't wired up — this only shows the
      // stored file and accepts a replacement locally.
      return (
        <span>
          {stringValue && <span>{stringValue}</span>}
          <input
            id={id}
            type="file"
            accept={field.accept}
            disabled={disabled}
            onChange={(e) => onChange(e.target.files?.[0] ?? null)}
          />
        </span>
      );

    case "file_multi":
      return (
        <input
          id={id}
          type="file"
          accept={field.accept}
          multiple
          disabled={disabled}
          onChange={(e) => onChange(e.target.files ? [...e.target.files] : [])}
        />
      );

    case "signature":
      return (
        <SignaturePad
          ariaLabel={field.label ?? field.field}
          value={typeof value === "string" ? value : null}
          onChange={onChange}
          disabled={disabled}
        />
      );

    case "barcode":
      // Camera-driven scanning needs a WASM decoder + camera-access
      // library, neither chosen yet — manual code entry stands in.
      return (
        <input
          id={id}
          type="text"
          value={stringValue}
          placeholder="Scan or enter code"
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        />
      );

    case "qr_code":
      return stringValue ? <span>{stringValue}</span> : null;

    case "rating": {
      const max = field.max ?? 5;
      const current = typeof value === "number" ? value : 0;
      return (
        <span role="radiogroup" aria-label={field.label ?? field.field}>
          {Array.from({ length: max }, (_, i) => i + 1).map((n) => (
            <button
              key={n}
              type="button"
              disabled={disabled}
              aria-pressed={n <= current}
              aria-label={`${n} star${n === 1 ? "" : "s"}`}
              onClick={() => onChange(n)}
            >
              {n <= current ? "★" : "☆"}
            </button>
          ))}
        </span>
      );
    }

    case "slider":
      return (
        <input
          id={id}
          type="range"
          min={field.min ?? 0}
          max={field.max ?? 100}
          step={field.step ?? 1}
          value={typeof value === "number" ? value : (field.min ?? 0)}
          disabled={disabled}
          onChange={(e) => onChange(Number(e.target.value))}
        />
      );

    case "json":
      return (
        <textarea
          id={id}
          value={value === undefined ? "" : JSON.stringify(value, null, 2)}
          rows={field.rows ?? 6}
          disabled={disabled}
          style={{ fontFamily: "monospace" }}
          onChange={(e) => {
            try {
              onChange(JSON.parse(e.target.value));
            } catch {
              // Invalid JSON mid-edit — ignored until it parses again.
            }
          }}
        />
      );

    case "address": {
      const mapping = field.address_fields ?? {};
      return (
        <span>
          {Object.entries(mapping).map(([part, addrField]) => (
            <input
              key={part}
              type="text"
              aria-label={part}
              placeholder={part}
              disabled={disabled}
              value={typeof record[addrField] === "string" ? (record[addrField] as string) : ""}
              onChange={(e) => onChange({ [addrField]: e.target.value })}
            />
          ))}
        </span>
      );
    }

    case "location": {
      // No map library chosen yet — lat/lng entered directly.
      const coords = value as { lat?: number; lng?: number } | undefined;
      return (
        <span>
          <input
            type="number"
            aria-label="latitude"
            value={coords?.lat ?? ""}
            disabled={disabled}
            onChange={(e) => onChange({ ...coords, lat: toNumber(e.target.value) })}
          />
          <input
            type="number"
            aria-label="longitude"
            value={coords?.lng ?? ""}
            disabled={disabled}
            onChange={(e) => onChange({ ...coords, lng: toNumber(e.target.value) })}
          />
        </span>
      );
    }

    case "separator":
      return <hr />;

    case "label":
      return <p>{field.label_text}</p>;

    case "computed_display":
      // `expression` isn't evaluated — the shell-side domain-expression
      // interpreter it needs doesn't exist yet (same gap as `condition`).
      return <span>{stringValue}</span>;

    case "custom":
      // No module component registry exists yet (defineModule().views) —
      // same fallback column-renderers.tsx already uses for "custom" columns.
      return <span>{stringValue}</span>;

    default:
      return (
        <input id={id} type="text" value={stringValue} disabled={disabled} onChange={(e) => onChange(e.target.value)} />
      );
  }
}
