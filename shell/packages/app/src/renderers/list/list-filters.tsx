import type { ListFilter } from "./list-view-types.js";
import type { FilterValue } from "./use-list-state.js";

// Only the "boolean" filter type is in scope here — the full filter-type
// catalog (select/multi_select/radio/date/...) is goerp#593's own job.
export interface BooleanFilterInputProps {
  filter: ListFilter;
  value: FilterValue | undefined;
  onChange: (value: FilterValue | undefined) => void;
}

export function booleanFilterState(value: FilterValue | undefined): "any" | "true" | "false" {
  if (value === true || value === "true") return "true";
  if (value === false || value === "false") return "false";
  return "any";
}

export function BooleanFilterInput({ filter, value, onChange }: BooleanFilterInputProps) {
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

export interface ListFiltersProps {
  filters: ListFilter[];
  values: Record<string, FilterValue>;
  onChange: (field: string, value: FilterValue | undefined) => void;
}

export function ListFilters({ filters, values, onChange }: ListFiltersProps) {
  const booleanFilters = filters.filter((f) => f.type === "boolean");
  if (booleanFilters.length === 0) return null;

  return (
    <div>
      {booleanFilters.map((filter) => (
        <BooleanFilterInput
          key={filter.field}
          filter={filter}
          value={values[filter.field]}
          onChange={(value) => onChange(filter.field, value)}
        />
      ))}
    </div>
  );
}
