import { useQueries } from "@tanstack/react-query";
import { apiClient } from "../http/index.js";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { ResourceRegistry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";

// manifest-spec.md's relation-column batch-fetch fallback (used when no
// `display_field` is set): one request per distinct resource/labelField
// pair, `filter[id][]=...`, resolving the display label for each id.
// Scoped to an explicit `labelField` only — auto-resolving the resource's
// own default label field needs the model/view registry goerp#636 already
// deferred to backlog #674.
export interface RelationBatchSpec {
  key: string;
  resource: string;
  labelField: string;
  ids: string[];
}

export function createRelationLabelsQueryOptions(
  spec: RelationBatchSpec,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "get"> = apiClient,
) {
  const uniqueIds = [...new Set(spec.ids)].filter(Boolean).sort();
  return {
    queryKey: ["relation-labels", spec.resource, spec.labelField, uniqueIds] as const,
    queryFn: async (): Promise<Record<string, string>> => {
      const entry = await registry.resolve(spec.resource);
      const response = await client.get<PagedResponse<Record<string, unknown>>>(entry.listPath, {
        params: { "filter[id][]": uniqueIds, limit: uniqueIds.length },
      });
      const labels: Record<string, string> = {};
      for (const row of response.data) {
        if (typeof row.id === "string") labels[row.id] = String(row[spec.labelField] ?? "");
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
