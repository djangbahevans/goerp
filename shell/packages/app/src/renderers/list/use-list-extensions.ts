import { extensionBatchLoaderRegistry } from "@goerp/sdk/module";
import { summarizeIssues, viewExtensionRegistry } from "@goerp/sdk/schema";
import { useQueries, useQuery } from "@tanstack/react-query";
import * as v from "valibot";
import { type ListColumn, ListColumnSchema, type ListFilter, ListFilterSchema, type Row } from "./list-view-types.js";

// A "columns" or "filter" extension merged into this view's own — split by
// declared position (view-system.md §10). `permission` on the parsed
// entry gates it directly (ListColumn.permission/ListFilter.permission),
// the same way an extension form tab honors its own `permission`.
export interface ListExtensions {
  prependColumns: ListColumn[];
  appendColumns: ListColumn[];
  prependFilters: ListFilter[];
  appendFilters: ListFilter[];
}

const EMPTY_EXTENSIONS: ListExtensions = {
  prependColumns: [],
  appendColumns: [],
  prependFilters: [],
  appendFilters: [],
};

// useListExtensions resolves the "columns"/"filter" extensions targeting
// `{module}.{viewName}`, already ordered dependencies-first by
// viewExtensionRegistry, into ready-to-render ListColumn/ListFilter
// entries. An entry whose definition didn't resolve, isn't a "columns"/
// "filter" type, or whose type-specific member doesn't match its schema is
// skipped — nothing renders, nothing throws (view-system.md §10).
export function useListExtensions(module: string, viewName: string) {
  return useQuery({
    queryKey: ["list-view-extensions", module, viewName],
    queryFn: async (): Promise<ListExtensions> => {
      const entries = await viewExtensionRegistry.forTarget(module, viewName);
      const extensions: ListExtensions = {
        prependColumns: [],
        appendColumns: [],
        prependFilters: [],
        appendFilters: [],
      };

      for (const entry of entries) {
        if (entry.definition?.type === "columns") {
          const result = v.safeParse(v.array(ListColumnSchema), entry.definition.columns);
          if (!result.success) {
            console.warn(
              `useListExtensions: "${entry.ref.extension}" in module "${entry.module}" declares a "columns" extension whose columns member doesn't match ListColumn[] (manifest-spec.md §11) — ${summarizeIssues(result.issues)}`,
            );
            continue;
          }
          (entry.definition.position === "prepend" ? extensions.prependColumns : extensions.appendColumns).push(
            ...result.output,
          );
        } else if (entry.definition?.type === "filter") {
          const result = v.safeParse(ListFilterSchema, entry.definition.filter);
          if (!result.success) {
            console.warn(
              `useListExtensions: "${entry.ref.extension}" in module "${entry.module}" declares a "filter" extension whose filter member doesn't match ListFilter (manifest-spec.md §11) — ${summarizeIssues(result.issues)}`,
            );
            continue;
          }
          (entry.definition.position === "prepend" ? extensions.prependFilters : extensions.appendFilters).push(
            result.output,
          );
        }
      }

      return extensions;
    },
  });
}

// Reads with EMPTY_EXTENSIONS as a not-yet-loaded/errored fallback — a
// column/filter extension appearing a render late (once the query
// resolves) is the same lazy-arrival ViewTabContent already accepts for
// form tab extensions, not a state ListRenderer needs to block on.
export function extensionsOrEmpty(data: ListExtensions | undefined): ListExtensions {
  return data ?? EMPTY_EXTENSIONS;
}

// namespacedField reports whether field carries the `{module}:` prefix
// view-system.md §10's extension columns use — the same signal
// dispatch_filter.go's compileListFilter uses to recognize an
// extension-owned filter key.
export function isNamespacedField(field: string): boolean {
  return field.includes(":");
}

// useExtensionColumnRowData batch-loads extension column values for
// already-rendered pages, one query per page (relationBatchSpecs' own
// per-page caching strategy: each page's id set never changes once
// fetched, so its query stays cached forever regardless of later pages
// loading). Runs only when at least one namespaced (extension) column is
// currently rendered, and only when the extending module actually
// registered a loader for this view — an unregistered/unloaded module
// leaves extension columns simply blank, never throws.
export function useExtensionColumnRowData(
  module: string,
  viewName: string,
  columns: ListColumn[],
  pages: Row[][],
): Map<string, Record<string, unknown>> {
  const hasExtensionColumns = columns.some((c) => isNamespacedField(c.field));
  const loaderKey = `${module}.${viewName}`;

  const results = useQueries({
    queries: pages.map((pageRows) => {
      const ids = [
        ...new Set(pageRows.map((row) => row.id).filter((id): id is string => typeof id === "string")),
      ].sort();
      return {
        queryKey: ["list-extension-column-data", loaderKey, ids],
        queryFn: () => extensionBatchLoaderRegistry.resolve(loaderKey)(ids),
        enabled: hasExtensionColumns && ids.length > 0 && extensionBatchLoaderRegistry.has(loaderKey),
      };
    }),
  });

  const merged = new Map<string, Record<string, unknown>>();
  for (const result of results) {
    if (!result.data) continue;
    for (const [id, fields] of result.data) merged.set(id, { ...merged.get(id), ...fields });
  }
  return merged;
}
