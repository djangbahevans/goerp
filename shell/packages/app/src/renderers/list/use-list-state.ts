import type { FilterParamValue, FilterRange } from "@goerp/sdk";
import { flattenFilterParams } from "@goerp/sdk";
import type { SavedFilter } from "@goerp/sdk/react";
import { useSavedFilters } from "@goerp/sdk/react";
import { defaultParseSearch, useNavigate, useSearch } from "@tanstack/react-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { isMultiValueFilter } from "./list-filters.js";
import type { ListFilter, ListViewDeclaration } from "./list-view-types.js";

// Filter/sort/group-by state for a list view: full-page mode sources it
// from URL query params, embedded mode from local React state — never
// both. A scalar value read back from the URL isn't guaranteed to stay a
// string — TanStack Router's default parser coerces "true"/"false"/
// numeric query values into real booleans/numbers. Reuses
// @goerp/sdk's FilterParamValue so URL round-tripping and the actual
// request (useInfiniteList) serialize filters identically.
export type FilterValue = FilterParamValue;

export interface ListState {
  filter: Record<string, FilterValue>;
  sort: string | undefined;
  groupBy: string | undefined;
}

export interface ListStateHandle extends ListState {
  setFilter: (field: string, value: FilterValue | undefined) => void;
  setFilters: (values: Record<string, FilterValue | undefined>) => void;
  setSort: (sort: string | undefined) => void;
  setGroupBy: (field: string | undefined) => void;
}

// erp-design.md §11.4: `filter[field]` (implicit eq) or `filter[field][op]`.
const FILTER_KEY_PATTERN = /^filter\[([^[\]]+)\](?:\[([^[\]]+)\])?$/;

function isScalarFilterValue(value: unknown): value is string | number | boolean {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean";
}

function isListStateKey(key: string): boolean {
  return key === "sort" || key === "group_by" || FILTER_KEY_PATTERN.test(key);
}

// Whether the raw (pre-defaultSort-fallback) URL search carries any
// filter[...]/sort/group_by key at all — the "explicit URL params" tier
// of view-system.md §4's default-filter precedence needs this distinct
// from parseListSearch's own output, since listState.sort is never
// undefined by the time a caller reads it (useFullPageListState already
// backfills a missing sort with defaultSort).
export function hasExplicitListState(search: Record<string, unknown>): boolean {
  return Object.keys(search).some(isListStateKey);
}

export function parseListSearch(search: Record<string, unknown>): ListState {
  const filter: Record<string, FilterValue> = {};
  const ranges: Record<string, FilterRange> = {};

  for (const [key, value] of Object.entries(search)) {
    const match = FILTER_KEY_PATTERN.exec(key);
    if (!match) continue;
    const field = match[1] as string;
    const op = match[2];

    if (op === "in") {
      // A single-value `in` list isn't distinguishable from a lone scalar
      // once TanStack Router's parser has coerced it — normalize through
      // String() so a numeric or boolean value doesn't vanish.
      if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
        const raw = String(value);
        if (raw !== "") filter[field] = raw.split(",");
      }
    } else if (op === "gte" || op === "lte") {
      if (typeof value === "string" || typeof value === "number") {
        ranges[field] = { ...ranges[field], [op]: String(value) };
      }
    } else if (op === "like") {
      if (typeof value === "string" || typeof value === "number") {
        filter[field] = { like: String(value).replace(/^%|%$/g, "") };
      }
    } else if (op === "isnull") {
      // TanStack Router's parser already coerces "true"/"false" to real
      // booleans (this file's own top comment) — the string fallback below
      // just matches every other branch's defensive style.
      if (value === true || value === "true") filter[field] = { isnull: true };
      else if (value === false || value === "false") filter[field] = { isnull: false };
    } else if (!op && isScalarFilterValue(value)) {
      filter[field] = value;
    }
  }

  for (const [field, range] of Object.entries(ranges)) {
    filter[field] = range;
  }

  return {
    filter,
    sort: typeof search.sort === "string" ? search.sort : undefined,
    groupBy: typeof search.group_by === "string" ? search.group_by : undefined,
  };
}

export function listStateToSearch(state: ListState): Record<string, string | number | boolean> {
  const search = flattenFilterParams(state.filter);
  if (state.sort !== undefined) search.sort = state.sort;
  if (state.groupBy !== undefined) search.group_by = state.groupBy;
  return search;
}

// Replaces only this list's own filter[...]/sort/group_by keys in `prev`,
// keeping every other search param (active tab, another list on the same
// page, etc.) untouched — TanStack Router's search updater replaces the
// whole object otherwise.
function mergeListStateIntoSearch(prev: Record<string, unknown>, state: ListState): Record<string, unknown> {
  const preserved: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(prev)) {
    if (!isListStateKey(key)) preserved[key] = value;
  }
  return { ...preserved, ...listStateToSearch(state) };
}

function applyFilterUpdates(
  filter: Record<string, FilterValue>,
  updates: Record<string, FilterValue | undefined>,
): Record<string, FilterValue> {
  const next = { ...filter };
  for (const [field, value] of Object.entries(updates)) {
    if (value === undefined) delete next[field];
    else next[field] = value;
  }
  return next;
}

function useEmbeddedListState(defaultSort: string | undefined): ListStateHandle {
  const [state, setState] = useState<ListState>({ filter: {}, sort: defaultSort, groupBy: undefined });

  const setFilters = useCallback((updates: Record<string, FilterValue | undefined>) => {
    setState((prev) => ({ ...prev, filter: applyFilterUpdates(prev.filter, updates) }));
  }, []);

  const setFilter = useCallback(
    (field: string, value: FilterValue | undefined) => setFilters({ [field]: value }),
    [setFilters],
  );

  const setSort = useCallback((sort: string | undefined) => {
    setState((prev) => ({ ...prev, sort }));
  }, []);

  const setGroupBy = useCallback((groupBy: string | undefined) => {
    setState((prev) => ({ ...prev, groupBy }));
  }, []);

  return { ...state, setFilter, setFilters, setSort, setGroupBy };
}

// ListRenderer is decoupled from any specific declared route, so there's
// no route-level search validator for TanStack Router to type this
// against — cast once here rather than sprinkling `any` through every call site.
type NavigateSearch = (updater: (prev: Record<string, unknown>) => Record<string, unknown>) => Promise<void>;

function useFullPageListState(defaultSort: string | undefined): ListStateHandle {
  const search = useSearch({ strict: false }) as Record<string, unknown>;
  const navigate = useNavigate() as unknown as (opts: { search: Parameters<NavigateSearch>[0] }) => Promise<void>;

  const state = useMemo(() => {
    const parsed = parseListSearch(search);
    return parsed.sort === undefined ? { ...parsed, sort: defaultSort } : parsed;
  }, [search, defaultSort]);

  const setFilters = useCallback(
    (updates: Record<string, FilterValue | undefined>) => {
      void navigate({
        search: (prev) => {
          const next = parseListSearch(prev);
          const filter = applyFilterUpdates(next.filter, updates);
          return mergeListStateIntoSearch(prev, { ...next, filter });
        },
      });
    },
    [navigate],
  );

  const setFilter = useCallback(
    (field: string, value: FilterValue | undefined) => setFilters({ [field]: value }),
    [setFilters],
  );

  const setSort = useCallback(
    (sort: string | undefined) => {
      void navigate({
        search: (prev) => mergeListStateIntoSearch(prev, { ...parseListSearch(prev), sort }),
      });
    },
    [navigate],
  );

  const setGroupBy = useCallback(
    (groupBy: string | undefined) => {
      void navigate({
        search: (prev) => mergeListStateIntoSearch(prev, { ...parseListSearch(prev), groupBy }),
      });
    },
    [navigate],
  );

  return { ...state, setFilter, setFilters, setSort, setGroupBy };
}

export function useListState(embedded: boolean | undefined, defaultSort: string | undefined): ListStateHandle {
  // Both hooks run unconditionally every render — `embedded` doesn't
  // change across a given ListRenderer instance's lifetime, so this
  // doesn't violate the rules of hooks in practice.
  const embeddedState = useEmbeddedListState(defaultSort);
  const fullPageState = useFullPageListState(defaultSort);
  return embedded ? embeddedState : fullPageState;
}

// Accepts numeric bounds too — manifest-spec.md types `default`/
// `default_filters` as `any`, and a number_range filter's natural default
// is numeric, not pre-stringified.
function coerceRangeBounds(raw: unknown): FilterRange | undefined {
  if (typeof raw !== "object" || raw === null) return undefined;
  const { gte, lte } = raw as { gte?: unknown; lte?: unknown };
  const range: FilterRange = {};
  if (typeof gte === "string" || typeof gte === "number") range.gte = String(gte);
  if (typeof lte === "string" || typeof lte === "number") range.lte = String(lte);
  return Object.keys(range).length > 0 ? range : undefined;
}

// A `Filter.default`'s raw JSON value, wrapped to match how its type
// serializes (manifest-spec.md's `default` field carries no shape info of
// its own).
function coerceFilterDefault(filter: ListFilter, raw: unknown): FilterValue | undefined {
  if (filter.type === "text") return typeof raw === "string" ? { like: raw } : undefined;
  if (filter.type === "daterange" || filter.type === "number_range") return coerceRangeBounds(raw);
  if (isMultiValueFilter(filter)) return Array.isArray(raw) ? raw.map(String) : undefined;
  if (typeof raw === "string" || typeof raw === "number" || typeof raw === "boolean") return raw;
  return undefined;
}

// `default_filters` values have no per-field type context the way a
// Filter object's own `default` does — a range-shaped object is inferred
// from its own {gte,lte} shape rather than a declared filter type.
function coerceDefaultFiltersValue(raw: unknown): FilterValue | undefined {
  if (Array.isArray(raw)) return raw.map(String);
  if (typeof raw === "string" || typeof raw === "number" || typeof raw === "boolean") return raw;
  return coerceRangeBounds(raw);
}

// manifest-spec.md §9.1: default_filters wins over a Filter's own default on the same field.
// Takes only the fields it touches, not the full ListViewDeclaration, so KanbanViewDeclaration/PivotViewDeclaration can reuse it too.
export function computeDefaultFilters(
  view: Pick<ListViewDeclaration, "default_filters" | "filters">,
): Record<string, FilterValue> {
  const defaults: Record<string, FilterValue> = {};

  for (const [field, raw] of Object.entries(view.default_filters ?? {})) {
    const value = coerceDefaultFiltersValue(raw);
    if (value !== undefined) defaults[field] = value;
  }

  for (const filter of view.filters ?? []) {
    if (filter.default === undefined || filter.field in defaults) continue;
    const value = coerceFilterDefault(filter, filter.default);
    if (value !== undefined) defaults[filter.field] = value;
  }

  return defaults;
}

// view-system.md §4: a user's own is_default saved filter beats the manifest's default_filters.
// Replays the saved queryString through defaultParseSearch (the same parser the router itself runs) rather than hand-rolling coercion.
export function resolveDefaultSavedFilterState(filters: SavedFilter[]): ListState | undefined {
  const defaultFilter = filters.find((filter) => filter.isDefault);
  if (!defaultFilter) return undefined;
  return parseListSearch(defaultParseSearch(defaultFilter.queryString));
}

// view-system.md §4's default-filter precedence, shared by List/Kanban/Pivot: explicit state wins, else a user's own is_default saved filter, else the manifest's default_filters.
// applySortAndGroupBy is true only for ListRenderer — Kanban/Pivot never read listState.sort/groupBy, since grouping/shape there comes from the manifest.
export function useDefaultFilterApplication(
  view: Pick<ListViewDeclaration, "name" | "default_filters" | "filters">,
  listState: ListStateHandle,
  embedded: boolean | undefined,
  applySortAndGroupBy = false,
): void {
  // select avoids re-rendering on unrelated search-param changes, and lets
  // Kanban/Pivot (applySortAndGroupBy false) skip the raw-search check
  // below without paying for a second, discarded subscription.
  const hasExplicitUrlState = useSearch({
    strict: false,
    select: (search: Record<string, unknown>) => hasExplicitListState(search),
  });
  const savedFiltersView = useSavedFilters(view.name, { enabled: !embedded });

  const defaultsApplied = useRef(false);

  // Monotonic: once true, stays true — distinguishes "the caller had
  // nothing to begin with" from "the user cleared it back to empty" (the
  // original mount-time-only check). Re-derived from this render's live
  // values on every render, not just at mount, so an edit that lands after
  // mount but before savedFiltersView resolves is treated the same as if
  // it had already been present at mount. Done here in the render body
  // rather than in a separate effect so it's guaranteed to run before the
  // gated effect below on any given commit — two effects would depend on
  // declaration order to get that right. (An edit whose own state update
  // hasn't yet committed to a render at the exact instant savedFiltersView
  // resolves is still a narrow, undetectable residual — same class of
  // accepted gap as ReplaceProfile's concurrent-first-save race.)
  const hadExplicitState = useRef(false);
  if (
    !defaultsApplied.current &&
    (Object.keys(listState.filter).length > 0 || (!embedded && applySortAndGroupBy && hasExplicitUrlState))
  ) {
    hadExplicitState.current = true;
  }

  const { setFilters, setSort, setGroupBy } = listState;
  // biome-ignore lint/correctness/useExhaustiveDependencies: guarded by defaultsApplied, gated on savedFiltersView.isLoading; the rest (view/setFilters/setSort/setGroupBy/applySortAndGroupBy/savedFiltersView.filters/hadExplicitState) are deliberately read only once that gate opens, not tracked as change-triggers.
  useEffect(() => {
    if (defaultsApplied.current) return;
    if (!embedded && savedFiltersView.isLoading) return;
    defaultsApplied.current = true;
    if (hadExplicitState.current) return;

    const savedDefault = embedded ? undefined : resolveDefaultSavedFilterState(savedFiltersView.filters);
    if (savedDefault) {
      if (Object.keys(savedDefault.filter).length > 0) setFilters(savedDefault.filter);
      if (applySortAndGroupBy && savedDefault.sort !== undefined) setSort(savedDefault.sort);
      if (applySortAndGroupBy && savedDefault.groupBy !== undefined) setGroupBy(savedDefault.groupBy);
      return;
    }

    const defaults = computeDefaultFilters(view);
    if (Object.keys(defaults).length > 0) setFilters(defaults);
  }, [embedded, savedFiltersView.isLoading]);
}
