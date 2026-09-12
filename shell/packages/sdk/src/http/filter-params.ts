// erp-design.md §11.4's structured filter[field]/filter[field][op] convention,
// shared by useInfiniteList's request params and the app package's list-view
// URL serialization (the same wire shape either way): a scalar is the
// implicit `eq`; a non-empty array comma-joins under `in`; `{ like }` wraps
// in `%...%` under `like`; `{ gte, lte }` sends up to two separate params;
// `{ isnull }` sends the field's IS NULL/IS NOT NULL check (goerp#791) with
// no comparison value at all.
export type FilterRange = { gte?: string; lte?: string };
export type FilterLike = { like: string };
export type FilterIsNull = { isnull: boolean };
export type FilterParamValue = string | number | boolean | string[] | FilterRange | FilterLike | FilterIsNull;

// Exported so callers needing to narrow an already-received FilterValue
// (e.g. list-filters.tsx's per-filter-type input components) share these
// predicates rather than re-deriving the same shape checks by hand.
export function isFilterLike(value: FilterParamValue): value is FilterLike {
  return typeof value === "object" && value !== null && !Array.isArray(value) && "like" in value;
}

export function isFilterIsNull(value: FilterParamValue): value is FilterIsNull {
  return typeof value === "object" && value !== null && !Array.isArray(value) && "isnull" in value;
}

export function isFilterRange(value: FilterParamValue): value is FilterRange {
  return (
    typeof value === "object" && value !== null && !Array.isArray(value) && !("like" in value) && !("isnull" in value)
  );
}

export function flattenFilterParams(
  filter: Record<string, FilterParamValue> | undefined,
): Record<string, string | number | boolean> {
  const flat: Record<string, string | number | boolean> = {};
  for (const [field, value] of Object.entries(filter ?? {})) {
    if (Array.isArray(value)) {
      if (value.length > 0) flat[`filter[${field}][in]`] = value.join(",");
    } else if (isFilterLike(value)) {
      if (value.like !== "") flat[`filter[${field}][like]`] = `%${value.like}%`;
    } else if (isFilterIsNull(value)) {
      flat[`filter[${field}][isnull]`] = value.isnull;
    } else if (isFilterRange(value)) {
      if (value.gte !== undefined && value.gte !== "") flat[`filter[${field}][gte]`] = value.gte;
      if (value.lte !== undefined && value.lte !== "") flat[`filter[${field}][lte]`] = value.lte;
    } else {
      flat[`filter[${field}]`] = value;
    }
  }
  return flat;
}
