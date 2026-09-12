import { apiClient } from "@goerp/sdk";
import type { FileValue, RelationValue, TagValue } from "@goerp/sdk/components";
import {
  CodeField,
  ColorPicker,
  CountrySelect,
  DateField,
  DateTimeField,
  FileField,
  LanguageSelect,
  MoneyField,
  RelationPicker,
  RichTextField,
  Select,
  SignaturePad,
  TagsField,
  TimeField,
  ToggleField,
} from "@goerp/sdk/components";
import { createInfiniteListQueryOptions, createRelationLabelsQueryOptions } from "@goerp/sdk/react";
import { resourceMetadataRegistry, resourceRegistry } from "@goerp/sdk/schema";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { Row } from "../list/list-view-types.js";
import type { FieldType, FormField } from "./form-view-types.js";

export type { TagValue } from "@goerp/sdk/components";

// "tags" writes `{field}` (an `_ids` key) as a UUID array but reads the
// plural key with `_ids` stripped ("tags"), holding full {id,name,color}.
function tagsReadKey(field: string): string {
  return field.replace(/_ids$/, "s");
}

function isTagValue(value: unknown): value is TagValue {
  return typeof value === "object" && value !== null && "id" in value && "name" in value;
}

const RELATION_LIKE_TYPES = new Set<FieldType>(["relation", "many2many", "user_select"]);

// go-sdk-reference.md §22 Many2One/Many2Many: the API embeds a companion
// object under the field name with its own _id/_ids suffix stripped
// ("customer_id" -> "customer", "tag_ids" -> "tags"), holding
// {id, display_name} — or an array of those for many2many/user_select with
// `multiple` set.
function relationReadKey(field: string): string {
  if (field.endsWith("_ids")) return `${field.slice(0, -4)}s`;
  if (field.endsWith("_id")) return field.slice(0, -3);
  return field;
}

function toRelationValue(row: unknown): RelationValue | null {
  if (typeof row !== "object" || row === null) return null;
  const record = row as Record<string, unknown>;
  const id = record.id;
  if (typeof id !== "string") return null;
  // A companion row with no display_name yet (e.g. not computed for this
  // record) still names a real relation — fall back to the id rather than
  // dropping the relation (and its id) entirely.
  const display = record.display_name;
  return { id, display: typeof display === "string" ? display : id };
}

function isRelationValue(value: unknown): value is RelationValue {
  return typeof value === "object" && value !== null && "id" in value && "display" in value;
}

const FILE_LIKE_TYPES = new Set<FieldType>(["file", "image", "avatar_upload", "file_multi"]);

// object-storage-guide.md §3/§8: model.File()/model.FileMulti() derive
// their read key the same way Many2One/Many2Many do (relationReadKey,
// above) — "signed_pdf_id" -> "signed_pdf", "attachment_ids" ->
// "attachments" — holding {id, name, content_type, size_bytes, url,
// url_expires_at}, or an array of those for file_multi.
function toFileValue(row: unknown): FileValue | null {
  if (typeof row !== "object" || row === null) return null;
  const record = row as Record<string, unknown>;
  const { id, name, content_type: contentType, size_bytes: sizeBytes, url } = record;
  if (
    typeof id !== "string" ||
    typeof name !== "string" ||
    typeof contentType !== "string" ||
    typeof sizeBytes !== "number"
  )
    return null;
  return { fileId: id, name, contentType, sizeBytes, url: typeof url === "string" ? url : undefined };
}

// manifest-spec.md's field-type table documents many2many as inherently a
// "Multi-select relation picker" — the type itself implies multiplicity,
// the same way FieldInput's own render switch already computes it
// (`field.multiple || type === "many2many"`) below. readFieldValue must
// agree: a many2many field declared without an explicit `multiple: true`
// still holds an array under its embedded companion key.
function isMultipleRelation(field: FormField): boolean {
  return field.multiple === true || field.type === "many2many";
}

export function readFieldValue(field: FormField, record: Row): unknown {
  if (field.type === "tags") return record[tagsReadKey(field.field)];
  if (field.type && RELATION_LIKE_TYPES.has(field.type)) {
    const raw = record[relationReadKey(field.field)];
    if (isMultipleRelation(field)) {
      return Array.isArray(raw) ? raw.map(toRelationValue).filter((v): v is RelationValue => v !== null) : [];
    }
    return toRelationValue(raw);
  }
  if (field.type && FILE_LIKE_TYPES.has(field.type)) {
    const raw = record[relationReadKey(field.field)];
    if (field.type === "file_multi") {
      return Array.isArray(raw) ? raw.map(toFileValue).filter((v): v is FileValue => v !== null) : [];
    }
    return toFileValue(raw);
  }
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

// `resourceRegistry` (goerp#638, CRUD-routes only) drives the fetch; the
// label field shown per option is a separate, caller-supplied concern.
function useResourceOptions(resource: string | undefined, enabled: boolean) {
  const query = useInfiniteQuery({
    ...createInfiniteListQueryOptions<Row>(resource ?? "", { limit: 100 }, resourceRegistry, apiClient),
    enabled: enabled && Boolean(resource),
  });
  return { data: query.data?.pages[0]?.data, isLoading: query.isLoading };
}

// "name" is a last-resort placeholder for the brief window before the
// registry resolves, or if the resource isn't nav-registered at all.
function useDefaultLabelField(resource: string | undefined, override: string | undefined): string {
  const query = useQuery({
    queryKey: ["resource-metadata-label-field", resource],
    queryFn: () => resourceMetadataRegistry.resolve(resource as string),
    enabled: Boolean(resource) && !override,
  });
  return override ?? query.data?.labelField ?? "name";
}

// select/multi_select-with-resource fields store a bare id (or id array),
// unlike relation/many2many/user_select's embedded display companion —
// manifest-spec.md §8b Strategy 3 resolves display text via batch fetch,
// reusing use-relation-labels.ts's shared query-options factory.
function useResolveByIds(
  resource: string | undefined,
  labelFieldOverride: string | undefined,
  ids: string[],
  enabled: boolean,
): Map<string, string> {
  const options = createRelationLabelsQueryOptions(
    {
      key: "value",
      resource: resource ?? "",
      ...(labelFieldOverride ? { labelField: labelFieldOverride } : {}),
      ids,
    },
    // Explicit, not the factory's own default params — same singleton
    // instances every other field renderer in this file uses (and the
    // same ones tests mock via vi.mock("@goerp/sdk", ...)).
    resourceMetadataRegistry,
    apiClient,
  );
  const query = useQuery({ ...options, enabled: enabled && Boolean(resource) && options.enabled });
  return new Map(Object.entries(query.data ?? {}));
}

function RelationInput({
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
  const needsResolve = field.type === "select" || field.type === "multi_select";
  const rawIds: string[] = !needsResolve
    ? []
    : field.multiple
      ? Array.isArray(value)
        ? value.map(String)
        : []
      : value == null
        ? []
        : [String(value)];
  const resolved = useResolveByIds(
    field.resource,
    field.resource_label_field,
    rawIds,
    needsResolve && rawIds.length > 0,
  );

  if (!field.resource) {
    return <span>{value == null ? "" : String(value)}</span>;
  }

  const pickerValue: RelationValue | RelationValue[] | null = needsResolve
    ? field.multiple
      ? rawIds.map((rid) => ({ id: rid, display: resolved.get(rid) ?? rid }))
      : rawIds[0] !== undefined
        ? { id: rawIds[0], display: resolved.get(rawIds[0]) ?? rawIds[0] }
        : null
    : field.multiple
      ? Array.isArray(value)
        ? value.filter(isRelationValue)
        : []
      : isRelationValue(value)
        ? value
        : null;

  const handleChange = (next: RelationValue | RelationValue[] | null) => {
    onChange(Array.isArray(next) ? next.map((v) => v.id) : (next?.id ?? null));
  };

  return (
    <RelationPicker
      id={id}
      resource={field.resource}
      labelField={field.resource_label_field}
      resourceFilter={field.resource_filter}
      value={pickerValue}
      onChange={handleChange}
      multiple={field.multiple ?? false}
      disabled={disabled}
      placeholder={`Search ${field.label ?? field.field}…`}
      // Explicit, not RelationPicker's own default params — keeps this on
      // the same apiClient/resourceMetadataRegistry singleton instance
      // every other field renderer in this file already uses (and the
      // same one tests mock via vi.mock("@goerp/sdk", ...)).
      client={apiClient}
      registry={resourceMetadataRegistry}
    />
  );
}

function isFileValue(value: unknown): value is FileValue {
  return typeof value === "object" && value !== null && "fileId" in value && "name" in value;
}

function FileInput({
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
  const multiple = field.type === "file_multi";
  const fieldValue: FileValue | FileValue[] | null = multiple
    ? Array.isArray(value)
      ? value.filter(isFileValue)
      : []
    : isFileValue(value)
      ? value
      : null;

  const handleChange = (next: FileValue | FileValue[] | null) => {
    onChange(Array.isArray(next) ? next.map((v) => v.fileId) : (next?.fileId ?? null));
  };

  return (
    <FileField
      id={id}
      variant={field.type === "image" ? "image" : field.type === "avatar_upload" ? "avatar" : "file"}
      multiple={multiple}
      value={fieldValue}
      onChange={handleChange}
      accept={field.accept}
      maxFileSizeMb={field.max_file_size_mb}
      disabled={disabled}
    />
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
  const labelField = useDefaultLabelField(field.resource, field.resource_label_field);
  const { data: rows } = useResourceOptions(field.resource, true);
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
  const selectValue: string | string[] = Array.isArray(value) ? value.map(String) : stringValue;

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
      return (
        // self-start: FieldWrapper's own <label> is flex flex-col — a bare
        // checkbox has no intrinsic width to resist the default cross-axis
        // stretch, so without this it renders full column width with the
        // native glyph centered inside that box, far from its own label.
        <input
          id={id}
          type="checkbox"
          className="self-start"
          checked={value === true}
          disabled={disabled}
          onChange={(e) => onChange(e.target.checked)}
        />
      );

    case "toggle":
      return <ToggleField id={id} value={value === true} onChange={onChange} disabled={disabled} />;

    case "select":
    case "multi_select":
      if (field.resource)
        return (
          <RelationInput
            field={{ ...field, multiple: field.multiple || type === "multi_select" }}
            id={id}
            value={value}
            onChange={onChange}
            disabled={disabled}
          />
        );
      return (
        <Select
          id={id}
          options={field.options ?? []}
          value={selectValue}
          onChange={(next) => onChange(next === "" ? undefined : next)}
          multiple={field.multiple || type === "multi_select"}
          placeholder={`Select ${field.label ?? field.field}…`}
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
        <RelationInput
          field={{ ...field, multiple: isMultipleRelation(field) }}
          id={id}
          value={value}
          onChange={onChange}
          disabled={disabled}
        />
      );

    case "tags":
      return <TagsInput field={field} id={id} value={value} onChange={onChange} disabled={disabled} />;

    case "user_select":
      return <RelationInput field={field} id={id} value={value} onChange={onChange} disabled={disabled} />;

    case "country_select":
      return (
        <CountrySelect
          id={id}
          value={stringValue}
          onChange={(code) => onChange(code === "" ? undefined : code)}
          placeholder="Select a country…"
          disabled={disabled}
        />
      );

    case "language_select":
      return (
        <LanguageSelect
          id={id}
          value={stringValue}
          onChange={(tag) => onChange(tag === "" ? undefined : tag)}
          placeholder="Select a language…"
          disabled={disabled}
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
    case "file_multi":
      return <FileInput field={field} id={id} value={value} onChange={onChange} disabled={disabled} />;

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
          className="font-mono"
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
