import { apiClient } from "@goerp/sdk";
import { createInfiniteListQueryOptions } from "@goerp/sdk/react";
import { resourceRegistry } from "@goerp/sdk/schema";
import { useInfiniteQuery } from "@tanstack/react-query";
import type { PointerEvent as ReactPointerEvent } from "react";
import { useEffect, useRef, useState } from "react";
import type { Row } from "../list/list-view-types.js";
import type { FieldOption, FormField } from "./form-view-types.js";

// "tags" writes `{field}` (an `_ids` key) as a UUID array but reads the
// plural key with `_ids` stripped ("tags"), holding full {id,name,color}.
function tagsReadKey(field: string): string {
  return field.replace(/_ids$/, "s");
}

export interface TagValue {
  id: string;
  name: string;
  color?: string;
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
  value,
  onChange,
  disabled,
}: {
  field: FormField;
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
  value,
  onChange,
  disabled,
}: {
  field: FormField;
  value: unknown;
  onChange: (value: unknown) => void;
  disabled: boolean;
}) {
  const labelField = field.resource_label_field ?? "name";
  const { data: options } = useResourceOptions(field.resource, labelField, true);
  // `value` (the plural read key) never reflects a pending edit, since
  // writes land under the singular `_ids` key — local state is the
  // display source of truth until the record itself reloads (a fresh
  // fetch or a post-save server response), which does change `value`.
  const [selected, setSelected] = useState<TagValue[]>(() => (Array.isArray(value) ? value.filter(isTagValue) : []));
  useEffect(() => {
    setSelected(Array.isArray(value) ? value.filter(isTagValue) : []);
  }, [value]);
  const selectedIds = new Set(selected.map((t) => t.id));

  if (!field.resource) {
    return <span>{selected.map((t) => t.name).join(", ")}</span>;
  }

  const commit = (next: TagValue[]) => {
    setSelected(next);
    onChange(next.map((t) => t.id));
  };

  return (
    <div>
      <span>
        {selected.map((tag) => (
          <span key={tag.id}>
            {tag.name}{" "}
            <button
              type="button"
              disabled={disabled}
              onClick={() => commit(selected.filter((t) => t.id !== tag.id))}
              aria-label={`Remove ${tag.name}`}
            >
              ×
            </button>
          </span>
        ))}
      </span>
      <select
        disabled={disabled}
        value=""
        onChange={(event) => {
          const row = (options ?? []).find((r) => String(r.id) === event.target.value);
          if (!row) return;
          commit([...selected, { id: String(row.id), name: String(row[labelField] ?? row.id) }]);
        }}
      >
        <option value="">Add {field.label ?? field.field}…</option>
        {(options ?? [])
          .filter((row) => !selectedIds.has(String(row.id)))
          .map((row) => (
            <option key={String(row.id)} value={String(row.id)}>
              {String(row[labelField] ?? row.id)}
            </option>
          ))}
      </select>
    </div>
  );
}

function OptionsSelect({
  field,
  options,
  value,
  onChange,
  disabled,
}: {
  field: FormField;
  options: FieldOption[];
  value: unknown;
  onChange: (value: unknown) => void;
  disabled: boolean;
}) {
  if (field.multiple) {
    const selected = new Set(Array.isArray(value) ? value.map(String) : []);
    return (
      <select
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
}

export function FieldInput({ field, value, onChange, record, disabled = false }: FieldInputProps) {
  const type = field.type ?? "text";
  const stringValue = typeof value === "string" ? value : value == null ? "" : String(value);

  switch (type) {
    case "text":
    case "email":
    case "phone":
    case "url":
      return (
        <input
          type={type === "text" ? "text" : type === "phone" ? "tel" : type}
          value={stringValue}
          placeholder={field.placeholder}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        />
      );

    case "textarea":
    case "rich_text":
    case "markdown":
    case "code":
      // rich_text/markdown/code fall back to a plain textarea — no editor
      // library chosen yet.
      return (
        <textarea
          value={stringValue}
          rows={field.rows ?? 3}
          placeholder={field.placeholder}
          disabled={disabled}
          style={type === "code" ? { fontFamily: "monospace" } : undefined}
          onChange={(e) => onChange(e.target.value)}
        />
      );

    case "number":
    case "integer":
      return (
        <input
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
        <span>
          {currency && <span>{currency} </span>}
          <input
            type="number"
            step={field.step ?? 0.01}
            min={field.min}
            max={field.max}
            value={stringValue}
            disabled={disabled}
            onChange={(e) => onChange(toNumber(e.target.value))}
          />
        </span>
      );
    }

    case "percent": {
      // "Stored as decimal 0.0-1.0 by convention" — the input shows/edits
      // whole percent points (0-100), converting on the way in/out.
      const percentValue = typeof value === "number" ? value * 100 : "";
      return (
        <input
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
      return <input type="date" value={stringValue} disabled={disabled} onChange={(e) => onChange(e.target.value)} />;
    case "datetime":
      return (
        <input
          type="datetime-local"
          value={stringValue}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    case "time":
      return <input type="time" value={stringValue} disabled={disabled} onChange={(e) => onChange(e.target.value)} />;

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
            value={value}
            onChange={onChange}
            disabled={disabled}
          />
        );
      return (
        <OptionsSelect
          field={{ ...field, multiple: field.multiple || type === "multi_select" }}
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
          value={value}
          onChange={onChange}
          disabled={disabled}
        />
      );

    case "tags":
      return <TagsInput field={field} value={value} onChange={onChange} disabled={disabled} />;

    case "user_select":
      return <ResourceSelect field={field} value={value} onChange={onChange} disabled={disabled} />;

    case "country_select":
    case "language_select":
      // No Intl enumeration API for regions/languages — free-text code
      // entry until a real dataset is wired in.
      return (
        <input
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
        return <input type="text" value={stringValue} disabled={disabled} onChange={(e) => onChange(e.target.value)} />;
      }
      return (
        <select disabled={disabled} value={stringValue} onChange={(e) => onChange(e.target.value)}>
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
      return (
        <input
          type="color"
          value={stringValue || "#000000"}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        />
      );

    case "icon_picker":
      // Free-text Lucide icon name — no icon-browsing picker UI yet.
      return (
        <input
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
          type="file"
          accept={field.accept}
          multiple
          disabled={disabled}
          onChange={(e) => onChange(e.target.files ? [...e.target.files] : [])}
        />
      );

    case "signature":
      return <SignaturePad value={stringValue} onChange={onChange} disabled={disabled} />;

    case "barcode":
      // Camera-driven scanning needs a WASM decoder + camera-access
      // library, neither chosen yet — manual code entry stands in.
      return (
        <input
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
      return <input type="text" value={stringValue} disabled={disabled} onChange={(e) => onChange(e.target.value)} />;
  }
}

function SignaturePad({
  value,
  onChange,
  disabled,
}: {
  value: string;
  onChange: (value: string) => void;
  disabled: boolean;
}) {
  // Pointer-driven canvas capture, no pressure-sensitivity or undo.
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const drawing = useRef(false);

  const pos = (e: ReactPointerEvent<HTMLCanvasElement>, canvas: HTMLCanvasElement) => {
    const rect = canvas.getBoundingClientRect();
    return { x: e.clientX - rect.left, y: e.clientY - rect.top };
  };

  const start = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    if (disabled) return;
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (!canvas || !ctx) return;
    drawing.current = true;
    const { x, y } = pos(e, canvas);
    ctx.beginPath();
    ctx.moveTo(x, y);
  };

  const move = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (!drawing.current || !canvas || !ctx) return;
    const { x, y } = pos(e, canvas);
    ctx.lineTo(x, y);
    ctx.stroke();
  };

  const end = () => {
    if (!drawing.current) return;
    drawing.current = false;
    const canvas = canvasRef.current;
    if (canvas) onChange(canvas.toDataURL("image/png"));
  };

  const clear = () => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (canvas && ctx) ctx.clearRect(0, 0, canvas.width, canvas.height);
    onChange("");
  };

  return (
    <span>
      {value ? (
        <img src={value} alt="Signature" style={{ maxWidth: 200 }} />
      ) : (
        <canvas
          ref={canvasRef}
          width={200}
          height={80}
          style={{ border: "1px solid", touchAction: "none" }}
          onPointerDown={start}
          onPointerMove={move}
          onPointerUp={end}
          onPointerLeave={end}
        />
      )}
      <button type="button" disabled={disabled} onClick={clear}>
        Clear
      </button>
    </span>
  );
}
