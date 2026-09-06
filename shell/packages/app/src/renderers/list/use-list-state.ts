import { useNavigate, useSearch } from "@tanstack/react-router";
import { useCallback, useMemo, useState } from "react";

// Filter/sort/group-by state for a list view: full-page mode sources it
// from URL query params, embedded mode from local React state — never
// both. A value read back from the URL isn't guaranteed to stay a
// string — TanStack Router's default parser coerces "true"/"false"/
// numeric query values into real booleans/numbers.
export type FilterValue = string | number | boolean;

export interface ListState {
  filter: Record<string, FilterValue>;
  sort: string | undefined;
  groupBy: string | undefined;
}

export interface ListStateHandle extends ListState {
  setFilter: (field: string, value: FilterValue | undefined) => void;
  setSort: (sort: string | undefined) => void;
  setGroupBy: (field: string | undefined) => void;
}

const FILTER_PREFIX = "filter[";

function isFilterValue(value: unknown): value is FilterValue {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean";
}

function isListStateKey(key: string): boolean {
  return key === "sort" || key === "group_by" || (key.startsWith(FILTER_PREFIX) && key.endsWith("]"));
}

export function parseListSearch(search: Record<string, unknown>): ListState {
  const filter: Record<string, FilterValue> = {};
  for (const [key, value] of Object.entries(search)) {
    if (key.startsWith(FILTER_PREFIX) && key.endsWith("]") && isFilterValue(value)) {
      filter[key.slice(FILTER_PREFIX.length, -1)] = value;
    }
  }
  return {
    filter,
    sort: typeof search.sort === "string" ? search.sort : undefined,
    groupBy: typeof search.group_by === "string" ? search.group_by : undefined,
  };
}

export function listStateToSearch(state: ListState): Record<string, FilterValue> {
  const search: Record<string, FilterValue> = {};
  for (const [field, value] of Object.entries(state.filter)) {
    search[`${FILTER_PREFIX}${field}]`] = value;
  }
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

function useEmbeddedListState(defaultSort: string | undefined): ListStateHandle {
  const [state, setState] = useState<ListState>({ filter: {}, sort: defaultSort, groupBy: undefined });

  const setFilter = useCallback((field: string, value: FilterValue | undefined) => {
    setState((prev) => {
      const filter = { ...prev.filter };
      if (value === undefined) delete filter[field];
      else filter[field] = value;
      return { ...prev, filter };
    });
  }, []);

  const setSort = useCallback((sort: string | undefined) => {
    setState((prev) => ({ ...prev, sort }));
  }, []);

  const setGroupBy = useCallback((groupBy: string | undefined) => {
    setState((prev) => ({ ...prev, groupBy }));
  }, []);

  return { ...state, setFilter, setSort, setGroupBy };
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

  const setFilter = useCallback(
    (field: string, value: FilterValue | undefined) => {
      void navigate({
        search: (prev) => {
          const next = parseListSearch(prev);
          const filter = { ...next.filter };
          if (value === undefined) delete filter[field];
          else filter[field] = value;
          return mergeListStateIntoSearch(prev, { ...next, filter });
        },
      });
    },
    [navigate],
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

  return { ...state, setFilter, setSort, setGroupBy };
}

export function useListState(embedded: boolean | undefined, defaultSort: string | undefined): ListStateHandle {
  // Both hooks run unconditionally every render — `embedded` doesn't
  // change across a given ListRenderer instance's lifetime, so this
  // doesn't violate the rules of hooks in practice.
  const embeddedState = useEmbeddedListState(defaultSort);
  const fullPageState = useFullPageListState(defaultSort);
  return embedded ? embeddedState : fullPageState;
}
