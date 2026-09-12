import type { APIClient, FilterParamValue, PagedResponse } from "@goerp/sdk";
import { apiClient, flattenFilterParams } from "@goerp/sdk";
import type { ResourceRegistry } from "@goerp/sdk/schema";
import { resourceRegistry } from "@goerp/sdk/schema";
import { useQueries } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import type { Row } from "./list-view-types.js";

// view-system.md §4 "Hierarchical lists (tree_field)" — see goerp#763 for
// the fetching strategy: root rows via filter[{treeField}][isnull]=true
// (goerp#791), a row's children via filter[{treeField}]=<row.id> on expand.
export interface TreeRow {
  row: Row;
  depth: number;
  isExpanded: boolean;
  isLoadingChildren: boolean;
  // Not yet confirmed childless — the expand affordance renders
  // optimistically until a fetch for this row comes back empty.
  hasChildrenUnknown: boolean;
  // Only meaningful once hasChildrenUnknown is false.
  hasChildren: boolean;
  // The children fetch for this row failed — isExpanded stays true so the
  // caller can render an error/retry affordance in place of children.
  hasError: boolean;
}

export interface ChildrenQuerySpec {
  parentId: string;
  resource: string;
  treeField: string;
  filter: Record<string, FilterParamValue>;
  sort: string | undefined;
}

// One non-paginated page of children per expanded node — no per-node
// "load more" in this ticket's scope, only the top-level list's own.
export function createTreeChildrenQueryOptions(
  spec: ChildrenQuerySpec,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "get"> = apiClient,
) {
  return {
    queryKey: ["tree-children", spec.resource, spec.treeField, spec.parentId, spec.filter, spec.sort ?? null] as const,
    queryFn: async (): Promise<Row[]> => {
      const entry = await registry.resolve(spec.resource);
      const response = await client.get<PagedResponse<Row>>(entry.listPath, {
        params: {
          ...flattenFilterParams({ ...spec.filter, [spec.treeField]: spec.parentId }),
          ...(spec.sort !== undefined ? { sort: spec.sort } : {}),
        },
      });
      return response.data;
    },
  };
}

// Ungated version of buildTreeRows, for the default_expanded_depth cascade
// below. `visited` guards against a cyclic tree_field chain.
function walkKnownDepths(rootRows: Row[], childrenByParentId: ReadonlyMap<string, Row[]>): Map<string, number> {
  const depths = new Map<string, number>();
  const visited = new Set<string>();
  function visit(rows: Row[], depth: number) {
    for (const row of rows) {
      if (typeof row.id !== "string" || visited.has(row.id)) continue;
      visited.add(row.id);
      depths.set(row.id, depth);
      const children = childrenByParentId.get(row.id);
      if (children) visit(children, depth + 1);
    }
  }
  visit(rootRows, 0);
  return depths;
}

// The flattened, depth-first render list — a collapsed row's already-
// fetched children stay cached in childrenByParentId but drop out here.
// `visited` guards against a cyclic tree_field chain the same way
// walkKnownDepths does above.
export function buildTreeRows(
  rootRows: Row[],
  childrenByParentId: ReadonlyMap<string, Row[]>,
  expandedIds: ReadonlySet<string>,
  loadingIds: ReadonlySet<string>,
  errorIds: ReadonlySet<string> = new Set(),
): TreeRow[] {
  const result: TreeRow[] = [];
  const visited = new Set<string>();
  function visit(rows: Row[], depth: number) {
    for (const row of rows) {
      const id = typeof row.id === "string" && !visited.has(row.id) ? row.id : undefined;
      if (id !== undefined) visited.add(id);
      const children = id !== undefined ? childrenByParentId.get(id) : undefined;
      result.push({
        row,
        depth,
        isExpanded: id !== undefined && expandedIds.has(id),
        isLoadingChildren: id !== undefined && loadingIds.has(id),
        hasChildrenUnknown: children === undefined,
        hasChildren: children !== undefined && children.length > 0,
        hasError: id !== undefined && errorIds.has(id),
      });
      if (id !== undefined && expandedIds.has(id) && children) {
        visit(children, depth + 1);
      }
    }
  }
  visit(rootRows, 0);
  return result;
}

export interface UseTreeRowsResult {
  treeRows: TreeRow[];
  toggleExpand: (rowId: string) => void;
  // Retries a row's own failed children fetch (TreeRow.hasError) without
  // collapsing/re-expanding it.
  retryChildren: (rowId: string) => void;
  // Each expanded node's own children fetch, one stable-id-set page per
  // node — excludes the root rows, which the caller already has as its own
  // real per-page breakdown (this hook only ever sees them pre-flattened).
  fetchedPages: Row[][];
}

export function useTreeRows(
  resource: string,
  treeField: string,
  defaultExpandedDepth: number,
  rootRows: Row[],
  filter: Record<string, FilterParamValue>,
  sort: string | undefined,
): UseTreeRowsResult {
  const [expandedIds, setExpandedIds] = useState<ReadonlySet<string>>(() => new Set());
  // Records every id default_expanded_depth has already auto-expanded, so
  // a later manual collapse sticks instead of being forced back open.
  const autoExpandedIdsRef = useRef(new Set<string>());

  // Resets expansion on a filter/sort change, so a stale parent id doesn't
  // keep firing children queries forever.
  const filterSortKey = JSON.stringify({ filter, sort });
  // biome-ignore lint/correctness/useExhaustiveDependencies: filterSortKey is the deliberate change-trigger, not read inside the effect.
  useEffect(() => {
    setExpandedIds(new Set());
    autoExpandedIdsRef.current = new Set();
  }, [filterSortKey]);

  const specs: ChildrenQuerySpec[] = [...expandedIds].map((parentId) => ({
    parentId,
    resource,
    treeField,
    filter,
    sort,
  }));
  const results = useQueries({ queries: specs.map((spec) => createTreeChildrenQueryOptions(spec)) });

  const childrenByParentId = new Map<string, Row[]>();
  const loadingIds = new Set<string>();
  const errorIds = new Set<string>();
  const refetchByParentId = new Map<string, () => void>();
  results.forEach((result, index) => {
    const parentId = specs[index]?.parentId;
    if (parentId === undefined) return;
    if (result.data !== undefined) childrenByParentId.set(parentId, result.data);
    // isFetching (not just isLoading) so a retry-after-error also shows the
    // loading state rather than the stale error alongside a busy retry.
    if (result.isLoading || result.isFetching) loadingIds.add(parentId);
    if (result.isError) errorIds.add(parentId);
    refetchByParentId.set(parentId, () => void result.refetch());
  });

  // childrenByParentId is derived fresh from `results` each render, not
  // held in state, so it's deliberately left out of the dependency list.
  // biome-ignore lint/correctness/useExhaustiveDependencies: see above.
  useEffect(() => {
    if (defaultExpandedDepth <= 0) return;
    const depths = walkKnownDepths(rootRows, childrenByParentId);
    const toExpand: string[] = [];
    for (const [id, depth] of depths) {
      if (depth < defaultExpandedDepth && !autoExpandedIdsRef.current.has(id)) {
        toExpand.push(id);
      }
    }
    if (toExpand.length === 0) return;
    for (const id of toExpand) autoExpandedIdsRef.current.add(id);
    setExpandedIds((current) => new Set([...current, ...toExpand]));
  }, [rootRows, results, defaultExpandedDepth]);

  function toggleExpand(rowId: string): void {
    setExpandedIds((current) => {
      const next = new Set(current);
      if (next.has(rowId)) next.delete(rowId);
      else next.add(rowId);
      return next;
    });
  }

  function retryChildren(rowId: string): void {
    refetchByParentId.get(rowId)?.();
  }

  return {
    treeRows: buildTreeRows(rootRows, childrenByParentId, expandedIds, loadingIds, errorIds),
    toggleExpand,
    retryChildren,
    fetchedPages: [...childrenByParentId.values()],
  };
}
