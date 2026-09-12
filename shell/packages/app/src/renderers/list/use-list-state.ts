import type { FilterParamValue, FilterRange } from "@goerp/sdk";
import { flattenFilterParams } from "@goerp/sdk";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useCallback, useMemo, useState } from "react";

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
