import { type QueryClient, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";
import { toast } from "../notifications/toast.js";

// typescript-sdk-reference.md's useSavedFilters — a dedicated hook, not
// useList, since saved filters aren't a module resource (no entry in the
// resource registry). Backed by the built-in /_meta/saved-filters
// endpoint (view-system.md §4 "Saved filters"), not a module route.
export interface SavedFilter {
  id: string;
  viewName: string;
  label: string;
  queryString: string;
  isDefault: boolean;
}

export interface UseSavedFiltersResult {
  filters: SavedFilter[];
  isLoading: boolean;
  save: (label: string) => Promise<void>;
  remove: (id: string) => Promise<void>;
  setDefault: (id: string) => Promise<void>;
}

// The engine's own wire shape (dispatch_meta.go's savedFilterResponse) —
// snake_case, converted to SavedFilter's camelCase below. No transform
// layer exists between apiClient and a caller, so every hook owns this
// mapping itself.
interface SavedFilterWire {
  id: string;
  view_name: string;
  label: string;
  query_string: string;
  is_default: boolean;
}

function toSavedFilter(wire: SavedFilterWire): SavedFilter {
  return {
    id: wire.id,
    viewName: wire.view_name,
    label: wire.label,
    queryString: wire.query_string,
    isDefault: wire.is_default,
  };
}

function savedFiltersQueryKey(viewName: string) {
  return ["saved-filters", viewName] as const;
}

type FetchClient = Pick<APIClient, "get">;
type MutationClient = Pick<APIClient, "post" | "patch" | "delete">;

export function createSavedFiltersQueryOptions(viewName: string, enabled = true, client: FetchClient = apiClient) {
  return {
    queryKey: savedFiltersQueryKey(viewName),
    enabled,
    queryFn: async (): Promise<SavedFilter[]> => {
      const { data } = await client.get<{ data: SavedFilterWire[] }>("/_meta/saved-filters", {
        params: { view_name: viewName },
      });
      return data.map(toSavedFilter);
    },
  };
}

function invalidateSavedFilters(queryClient: QueryClient, viewName: string): void {
  void queryClient.invalidateQueries({ queryKey: savedFiltersQueryKey(viewName) });
}

function reportError(err: AppError): void {
  toast.error(err.message);
}

export function createSavedFiltersSaveMutationOptions(
  viewName: string,
  queryClient: QueryClient,
  client: MutationClient = apiClient,
) {
  return {
    // Captures location.search automatically, per the hook's own contract
    // — the caller only supplies the label.
    mutationFn: (label: string) =>
      client.post<SavedFilterWire>("/_meta/saved-filters", {
        view_name: viewName,
        label,
        query_string: location.search,
        is_default: false,
      }),
    onSuccess: () => invalidateSavedFilters(queryClient, viewName),
    onError: reportError,
  };
}

export function createSavedFiltersRemoveMutationOptions(
  viewName: string,
  queryClient: QueryClient,
  client: MutationClient = apiClient,
) {
  return {
    mutationFn: (id: string) => client.delete<void>(`/_meta/saved-filters/${id}`),
    onSuccess: () => invalidateSavedFilters(queryClient, viewName),
    onError: reportError,
  };
}

export function createSavedFiltersSetDefaultMutationOptions(
  viewName: string,
  queryClient: QueryClient,
  client: MutationClient = apiClient,
) {
  return {
    mutationFn: (id: string) => client.patch<SavedFilterWire>(`/_meta/saved-filters/${id}`, { is_default: true }),
    onSuccess: () => invalidateSavedFilters(queryClient, viewName),
    onError: reportError,
  };
}

// options.enabled (default true) is an additive extension beyond
// typescript-sdk-reference.md's single-argument worked example — used by
// the generic renderers to skip the fetch entirely in embedded mode,
// where a saved default is never consulted (view-system.md's embedded
// contract: embedded views never touch the URL).
export function useSavedFilters(viewName: string, options: { enabled?: boolean } = {}): UseSavedFiltersResult {
  const queryClient = useQueryClient();
  const query = useQuery(createSavedFiltersQueryOptions(viewName, options.enabled ?? true));
  const saveMutation = useMutation(createSavedFiltersSaveMutationOptions(viewName, queryClient));
  const removeMutation = useMutation(createSavedFiltersRemoveMutationOptions(viewName, queryClient));
  const setDefaultMutation = useMutation(createSavedFiltersSetDefaultMutationOptions(viewName, queryClient));

  return {
    filters: query.data ?? [],
    isLoading: query.isLoading,
    save: async (label: string) => {
      await saveMutation.mutateAsync(label);
    },
    remove: async (id: string) => {
      await removeMutation.mutateAsync(id);
    },
    setDefault: async (id: string) => {
      await setDefaultMutation.mutateAsync(id);
    },
  };
}
