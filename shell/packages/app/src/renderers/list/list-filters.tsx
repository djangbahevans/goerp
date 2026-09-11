import type { FilterRange } from "@goerp/sdk";
import { apiClient } from "@goerp/sdk";
import type { RelationValue } from "@goerp/sdk/components";
import { CountrySelect, DateField, RelationPicker, Select } from "@goerp/sdk/components";
import { createRelationLabelsQueryOptions } from "@goerp/sdk/react";
import { resourceMetadataRegistry } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { useId } from "react";
import type { ListFilter } from "./list-view-types.js";
import type { FilterValue } from "./use-list-state.js";

interface FilterInputProps {
  filter: ListFilter;
  value: FilterValue | undefined;
  onChange: (value: FilterValue | undefined) => void;
}

// multi_select/tags are always multi; select/relation/user_select follow
// `filter.multiple` (manifest-spec.md documents `multiple` as valid on
// "select" too, not just the distinct "multi_select" type).
export function isMultiValueFilter(filter: ListFilter): boolean {
  return filter.type === "multi_select" || filter.type === "tags" || filter.multiple === true;
}

function asStringArray(value: FilterValue | undefined): string[] {
  return Array.isArray(value) ? value : [];
}

function asRange(value: FilterValue | undefined): FilterRange {
  return typeof value === "object" && value !== null && !Array.isArray(value) && !("like" in value) ? value : {};
}

function asLike(value: FilterValue | undefined): string {
  return typeof value === "object" && value !== null && !Array.isArray(value) && "like" in value ? value.like : "";
}

function asScalarString(value: FilterValue | undefined): string {
  if (typeof value === "string") return value;
  if (typeof value === "number") return String(value);
  return "";
}

function commitRangeBound(
  range: FilterRange,
  bound: "gte" | "lte",
  raw: string,
  onChange: (value: FilterValue | undefined) => void,
) {
  const next = { ...range };
  if (raw === "") delete next[bound];
  else next[bound] = raw;
  onChange(Object.keys(next).length > 0 ? next : undefined);
}

export function booleanFilterState(value: FilterValue | undefined): "any" | "true" | "false" {
  if (value === true || value === "true") return "true";
  if (value === false || value === "false") return "false";
  return "any";
}

export function BooleanFilterInput({ filter, value, onChange }: FilterInputProps) {
  return (
    <label>
      {filter.label}
      <select
        value={booleanFilterState(value)}
        onChange={(event) => {
          const next = event.target.value;
          onChange(next === "any" ? undefined : next === "true");
        }}
      >
        <option value="any">Any</option>
        <option value="true">Yes</option>
        <option value="false">No</option>
      </select>
    </label>
  );
}

function TextFilterInput({ filter, value, onChange }: FilterInputProps) {
  return (
    <label>
      {filter.label}
      <input
        type="text"
        value={asLike(value)}
        onChange={(event) => onChange(event.target.value === "" ? undefined : { like: event.target.value })}
      />
    </label>
  );
}

function StaticSelectFilterInput({ filter, value, onChange }: FilterInputProps) {
  const id = useId();
  const multiple = isMultiValueFilter(filter);
  const options = (filter.options ?? []).map((o) => ({
    value: o.value,
    label: o.label,
    color: o.color,
    disabled: o.disabled,
  }));

  return (
    <label htmlFor={id}>
      {filter.label}
      <Select
        id={id}
        options={options}
        multiple={multiple}
        value={multiple ? asStringArray(value) : asScalarString(value)}
        onChange={(next) => {
          if (Array.isArray(next)) onChange(next.length > 0 ? next : undefined);
          else onChange(next === "" ? undefined : next);
        }}
      />
    </label>
  );
}

function RadioFilterInput({ filter, value, onChange }: FilterInputProps) {
  const selected = asScalarString(value);
  return (
    <fieldset>
      <legend>{filter.label}</legend>
      <div role="radiogroup" aria-label={filter.label}>
        <label>
          <input
            type="radio"
            name={`filter-${filter.field}`}
            checked={selected === ""}
            onChange={() => onChange(undefined)}
          />
          Any
        </label>
        {(filter.options ?? []).map((option) => (
          <label key={option.value}>
            <input
              type="radio"
              name={`filter-${filter.field}`}
              checked={selected === option.value}
              disabled={option.disabled}
              onChange={() => onChange(option.value)}
            />
            {option.label}
          </label>
        ))}
      </div>
    </fieldset>
  );
}

function parseISODate(raw: string): Date | undefined {
  if (raw === "") return undefined;
  const date = new Date(raw);
  return Number.isNaN(date.getTime()) ? undefined : date;
}

function DateFilterInput({ filter, value, onChange }: FilterInputProps) {
  const id = useId();
  return (
    <label htmlFor={id}>
      {filter.label}
      <DateField
        id={id}
        value={parseISODate(asScalarString(value))}
        onChange={(date) => onChange(date ? date.toISOString().slice(0, 10) : undefined)}
      />
    </label>
  );
}

function DateRangeFilterInput({ filter, value, onChange }: FilterInputProps) {
  const range = asRange(value);
  return (
    <fieldset>
      <legend>{filter.label}</legend>
      <input
        type="date"
        aria-label={`${filter.label} from`}
        value={range.gte ?? ""}
        onChange={(event) => commitRangeBound(range, "gte", event.target.value, onChange)}
      />
      <input
        type="date"
        aria-label={`${filter.label} to`}
        value={range.lte ?? ""}
        onChange={(event) => commitRangeBound(range, "lte", event.target.value, onChange)}
      />
    </fieldset>
  );
}

function NumberFilterInput({ filter, value, onChange }: FilterInputProps) {
  return (
    <label>
      {filter.label}
      <input
        type="number"
        value={typeof value === "number" ? value : ""}
        onChange={(event) => onChange(event.target.value === "" ? undefined : Number(event.target.value))}
      />
    </label>
  );
}

function NumberRangeFilterInput({ filter, value, onChange }: FilterInputProps) {
  const range = asRange(value);
  return (
    <fieldset>
      <legend>{filter.label}</legend>
      <input
        type="number"
        aria-label={`${filter.label} min`}
        value={range.gte ?? ""}
        onChange={(event) => commitRangeBound(range, "gte", event.target.value, onChange)}
      />
      <input
        type="number"
        aria-label={`${filter.label} max`}
        value={range.lte ?? ""}
        onChange={(event) => commitRangeBound(range, "lte", event.target.value, onChange)}
      />
    </fieldset>
  );
}

function CountrySelectFilterInput({ filter, value, onChange }: FilterInputProps) {
  const id = useId();
  return (
    <label htmlFor={id}>
      {filter.label}
      <CountrySelect
        id={id}
        value={asScalarString(value) || undefined}
        onChange={(code) => onChange(code === "" ? undefined : code)}
      />
    </label>
  );
}

// A filter's currently-selected id(s) need a display label — the same
// gap field-renderers.tsx's RelationInput closes for form fields, reusing
// the same shared query-options factory (goerp#673).
function useResolveFilterLabels(resource: string | undefined, ids: string[]): Map<string, string> {
  const options = createRelationLabelsQueryOptions(
    { key: "value", resource: resource ?? "", ids },
    resourceMetadataRegistry,
    apiClient,
  );
  const query = useQuery({ ...options, enabled: Boolean(resource) && options.enabled });
  return new Map(Object.entries(query.data ?? {}));
}

// Shared by "relation"/"user_select" (single or multiple per `filter.multiple`)
// and "tags" (always multiple) — manifest-spec.md documents them with an
// identical resource/resource_filter/multiple contract.
function RelationFilterInput({ filter, value, onChange }: FilterInputProps) {
  const inputId = useId();
  const multiple = isMultiValueFilter(filter);
  const ids = multiple ? asStringArray(value) : typeof value === "string" ? [value] : [];
  const labels = useResolveFilterLabels(filter.resource, ids);

  if (!filter.resource) return null;

  const pickerValue: RelationValue | RelationValue[] | null = multiple
    ? ids.map((id) => ({ id, display: labels.get(id) ?? id }))
    : ids[0] !== undefined
      ? { id: ids[0], display: labels.get(ids[0]) ?? ids[0] }
      : null;

  return (
    <label htmlFor={inputId}>
      {filter.label}
      <RelationPicker
        id={inputId}
        resource={filter.resource}
        resourceFilter={filter.resource_filter}
        multiple={multiple}
        value={pickerValue}
        onChange={(next) => {
          if (Array.isArray(next)) onChange(next.length > 0 ? next.map((v) => v.id) : undefined);
          else onChange(next?.id);
        }}
        placeholder={`Search ${filter.label}…`}
        client={apiClient}
        registry={resourceMetadataRegistry}
      />
    </label>
  );
}

function TagsFilterInput({ filter, value, onChange }: FilterInputProps) {
  return <RelationFilterInput filter={{ ...filter, multiple: true }} value={value} onChange={onChange} />;
}

export interface ListFiltersProps {
  filters: ListFilter[];
  values: Record<string, FilterValue>;
  onChange: (field: string, value: FilterValue | undefined) => void;
}

export function ListFilters({ filters, values, onChange }: ListFiltersProps) {
  if (filters.length === 0) return null;

  return (
    <div>
      {filters.map((filter) => {
        const props: FilterInputProps = {
          filter,
          value: values[filter.field],
          onChange: (value) => onChange(filter.field, value),
        };
        switch (filter.type) {
          case "text":
            return <TextFilterInput key={filter.field} {...props} />;
          case "select":
          case "multi_select":
            return <StaticSelectFilterInput key={filter.field} {...props} />;
          case "radio":
            return <RadioFilterInput key={filter.field} {...props} />;
          case "boolean":
            return <BooleanFilterInput key={filter.field} {...props} />;
          case "date":
            return <DateFilterInput key={filter.field} {...props} />;
          case "daterange":
            return <DateRangeFilterInput key={filter.field} {...props} />;
          case "number":
            return <NumberFilterInput key={filter.field} {...props} />;
          case "number_range":
            return <NumberRangeFilterInput key={filter.field} {...props} />;
          case "relation":
          case "user_select":
            return <RelationFilterInput key={filter.field} {...props} />;
          case "tags":
            return <TagsFilterInput key={filter.field} {...props} />;
          case "country_select":
            return <CountrySelectFilterInput key={filter.field} {...props} />;
          default:
            return null;
        }
      })}
    </div>
  );
}
