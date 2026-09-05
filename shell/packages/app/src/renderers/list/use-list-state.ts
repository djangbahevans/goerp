import { useNavigate, useSearch } from "@tanstack/react-router";
import { useCallback, useMemo, useState } from "react";

// Filter/sort state for a list view, sourced from wherever the caller's
// mode says it should live — never both. shell-architecture.md §20:
// full-page mode reads/writes URL query params (`?filter[x]=y&sort=z`);
// embedded mode (a form's sub_list tab) keeps local React state so
// multiple embedded lists on one page never collide, and the tab never
// touches the URL at all.
// TanStack Router's default search parser coerces "true"/"false"/numeric
// query values into real booleans/numbers (qss.js's `toValue`), so a
// value read back from the URL isn't guaranteed to still be a string —
// only what survives one write→read round trip through the address bar.
export type FilterValue = string | number | boolean;

export interface ListState {
  filter: Record<string, FilterValue>;
  sort: string | undefined;
}

export interface ListStateHandle extends ListState {
  setFilter: (field: string, value: FilterValue | undefined) => void;
  setSort: (sort: string | undefined) => void;
}

const FILTER_PREFIX = "filter[";

function isFilterValue(value: unknown): value is FilterValue {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean";
}

export function parseListSearch(search: Record<string, unknown>): ListState {
  const filter: Record<string, FilterValue> = {};
  for (const [key, value] of Object.entries(search)) {
    if (key.startsWith(FILTER_PREFIX) && key.endsWith("]") && isFilterValue(value)) {
      filter[key.slice(FILTER_PREFIX.length, -1)] = value;
    }
  }
  return { filter, sort: typeof search.sort === "string" ? search.sort : undefined };
}

export function listStateToSearch(state: ListState): Record<string, FilterValue> {
  const search: Record<string, FilterValue> = {};
  for (const [field, value] of Object.entries(state.filter)) {
    search[`${FILTER_PREFIX}${field}]`] = value;
  }
  if (state.sort !== undefined) search.sort = state.sort;
  return search;
}

function useEmbeddedListState(defaultSort: string | undefined): ListStateHandle {
  const [state, setState] = useState<ListState>({ filter: {}, sort: defaultSort });

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

  return { ...state, setFilter, setSort };
}

// ListRenderer is decoupled from any specific declared route (goerp#636's
// own scope line), so there's no route-level search validator for
// TanStack Router to type this against — the registered router types
// `useNavigate`'s search updater as `never` for an unvalidated route.
// Cast once here rather than sprinkling `any` through every call site.
type NavigateSearch = (updater: (prev: Record<string, unknown>) => Record<string, FilterValue>) => Promise<void>;

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
          return listStateToSearch({ ...next, filter });
        },
      });
    },
    [navigate],
  );

  const setSort = useCallback(
    (sort: string | undefined) => {
      void navigate({
        search: (prev) => listStateToSearch({ ...parseListSearch(prev), sort }),
      });
    },
    [navigate],
  );

  return { ...state, setFilter, setSort };
}

export function useListState(embedded: boolean | undefined, defaultSort: string | undefined): ListStateHandle {
  // Both branches are hooks called unconditionally per render — `embedded`
  // is a prop that doesn't change across a given ListRenderer instance's
  // lifetime (it reflects where the view is mounted, not user interaction),
  // so this doesn't violate the rules of hooks in practice.
  const embeddedState = useEmbeddedListState(defaultSort);
  const fullPageState = useFullPageListState(defaultSort);
  return embedded ? embeddedState : fullPageState;
}
