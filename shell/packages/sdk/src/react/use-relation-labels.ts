import { useQueries } from "@tanstack/react-query";
import { apiClient } from "../http/index.js";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { BatchLoaderRegistry, ResourceMetadataRegistry } from "../schema/index.js";
import { batchLoaderRegistry, resourceListPath, resourceMetadataRegistry } from "../schema/index.js";

// manifest-spec.md §8b strategies 2/3. `view`, when set, is looked up in
// BatchLoaderRegistry as `${view}.${key}` — scoped per column, since a view
// can have several relation columns each needing a different loader.
// `labelField` overrides the registry default, matching
// ListColumn.resource_label_field.
export interface RelationBatchSpec {
  key: string;
  resource: string;
  labelField?: string;
  view?: string;
  ids: string[];
}

export function createRelationLabelsQueryOptions(
  spec: RelationBatchSpec,
  registry: Pick<ResourceMetadataRegistry, "resolve"> = resourceMetadataRegistry,
  client: Pick<APIClient, "get"> = apiClient,
  batchLoaders: Pick<BatchLoaderRegistry, "has" | "resolve"> = batchLoaderRegistry,
) {
  const uniqueIds = [...new Set(spec.ids)].filter(Boolean).sort();
  const loaderKey = spec.view ? `${spec.view}.${spec.key}` : undefined;
  return {
    queryKey: ["relation-labels", spec.resource, spec.labelField ?? "", loaderKey ?? "", uniqueIds] as const,
    queryFn: async (): Promise<Record<string, string>> => {
      if (loaderKey && batchLoaders.has(loaderKey)) {
        const labels = await batchLoaders.resolve(loaderKey)(uniqueIds);
        return Object.fromEntries(labels);
      }

      const entry = await registry.resolve(spec.resource);
      const path = entry && resourceListPath(entry);
      if (!entry || !path) return {}; // unregistered/unloaded module — caller falls back to the raw value.

      const labelField = spec.labelField ?? entry.labelField;
      const response = await client.get<PagedResponse<Record<string, unknown>>>(path, {
        params: { "filter[id][in]": uniqueIds.join(","), limit: uniqueIds.length },
      });
      const labels: Record<string, string> = {};
      for (const row of response.data) {
        if (typeof row.id === "string") labels[row.id] = String(row[labelField] ?? "");
      }
      return labels;
    },
    enabled: uniqueIds.length > 0,
  };
}

// Keyed by each spec's own `key` (typically the column field). Multiple
// specs may share one `key` (e.g. one per already-fetched page of an
// infinite list, each with that page's own small, stable id set): their
// results are merged rather than overwritten, so a spec whose ids never
// change once fetched (an earlier page) keeps its own cache entry and is
// never re-requested just because a later page added new, unrelated ids to
// the same column — see list-renderer.tsx's own per-page spec construction.
export function mergeLabelsByKey(
  specs: RelationBatchSpec[],
  results: (Record<string, string> | undefined)[],
): Map<string, Record<string, string>> {
  const labelsByKey = new Map<string, Record<string, string>>();
  specs.forEach((spec, index) => {
    labelsByKey.set(spec.key, { ...labelsByKey.get(spec.key), ...results[index] });
  });
  return labelsByKey;
}

// A variable number of queries without an unstable count of hook calls.
export function useRelationLabels(specs: RelationBatchSpec[]): Map<string, Record<string, string>> {
  const results = useQueries({ queries: specs.map((spec) => createRelationLabelsQueryOptions(spec)) });
  return mergeLabelsByKey(
    specs,
    results.map((r) => r.data),
  );
}
