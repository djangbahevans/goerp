import { ActionButton, EmptyState, Icon, Skeleton } from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import type { RelationBatchSpec } from "@goerp/sdk/react";
import {
  createInfiniteListQueryOptions,
  saveRecord,
  useInfiniteList,
  useRelationLabels,
  useSavedFilters,
} from "@goerp/sdk/react";
import { componentRegistry, resourceMetadataRegistry } from "@goerp/sdk/schema";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { fileUrlOf } from "../list/column-renderers.js";
import { ListActions } from "../list/list-actions.js";
import { ListFilters } from "../list/list-filters.js";
import { computeDefaultFilters, resolveDefaultSavedFilterState } from "../list/list-renderer.js";
import type { Row } from "../list/list-view-types.js";
import { useListState } from "../list/use-list-state.js";
import { resolveKanbanActionItems, useKanbanRouteAction } from "./kanban-actions.js";
import { KanbanBoard } from "./kanban-board.js";
import { bucketRowsByGroup, deriveGroupIds, splitCardFields } from "./kanban-grouping.js";
import type { KanbanViewDeclaration } from "./kanban-manifest-types.js";
import type { KanbanCardData, KanbanGroup } from "./kanban-view-types.js";

// view-system.md §6's GenericKanbanRenderer — resolves a manifest "type":
// "kanban" view (manifest-spec.md §9.3) into KanbanBoard's props.
export interface KanbanRendererProps {
  view: KanbanViewDeclaration;
  module: string;
  recordId?: string;
  embedded?: boolean;
  baseFilter?: Record<string, string>;
  showCreateAction?: boolean;
}

const DEFAULT_MAX_CARDS_PER_COLUMN = 50;

export function KanbanRenderer({
  view,
  module,
  recordId,
  embedded,
  baseFilter,
  showCreateAction,
}: KanbanRendererProps) {
  const listState = useListState(embedded, undefined);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const routeAction = useKanbanRouteAction();

  // Embedded views never touch the URL, so the "explicit URL params"
  // precedence tier doesn't apply to them, and there's nothing to fetch —
  // disabled rather than wasting a request whose result is never used.
  const savedFiltersView = useSavedFilters(view.name, { enabled: !embedded });

  // Snapshotted once at mount, not read live inside the effect below —
  // the effect's own run is deferred until savedFiltersView resolves, and
  // re-reading listState.filter live at that later point can no longer
  // distinguish "never had anything" from "the user already cleared it
  // back to empty" during the wait. Kanban never reads listState.sort/
  // groupBy (grouping is driven entirely by view.group_by), so unlike
  // ListRenderer this only ever needs to guard filter.
  const hadExplicitFilterOnMount = useRef(Object.keys(listState.filter).length > 0);

  // Applied once, waiting for savedFiltersView before applying either
  // kind of default (view-system.md §4's precedence rule), so a user's
  // own is_default saved filter can beat the manifest's default_filters
  // rather than racing it.
  const defaultsApplied = useRef(false);
  const { setFilters } = listState;
  // biome-ignore lint/correctness/useExhaustiveDependencies: guarded by defaultsApplied, gated on savedFiltersView.isLoading; view/setFilters/savedFiltersView.filters/hadExplicitFilterOnMount are deliberately read only once that gate opens, not tracked as change-triggers.
  useEffect(() => {
    if (defaultsApplied.current) return;
    if (!embedded && savedFiltersView.isLoading) return;
    defaultsApplied.current = true;
    if (hadExplicitFilterOnMount.current) return;

    const savedDefault = embedded ? undefined : resolveDefaultSavedFilterState(savedFiltersView.filters);
    if (savedDefault) {
      if (Object.keys(savedDefault.filter).length > 0) setFilters(savedDefault.filter);
      return;
    }

    const defaults = computeDefaultFilters(view);
    if (Object.keys(defaults).length > 0) setFilters(defaults);
  }, [embedded, savedFiltersView.isLoading]);

  // view-system.md's embedded-rendering contract: the locked base filter
  // always wins over user-driven state, never the other way around.
  const filter = { ...listState.filter, ...baseFilter };

  const infiniteListOptions = {
    filter,
    ...(embedded ? { cacheKeyPrefix: `embedded:${recordId ?? ""}:${view.name}` } : {}),
  };
  const { data, fetchNextPage, hasNextPage, isFetchingNextPage, isLoading, isError, error, refetch } =
    useInfiniteList<Row>(view.resource, infiniteListOptions);

  const rows = useMemo(() => (data?.pages.map((page) => page.data) ?? []).flat(), [data]);

  const [revealed, setRevealed] = useState<Record<string, number>>({});
  const maxPerColumn = view.max_cards_per_column ?? DEFAULT_MAX_CARDS_PER_COLUMN;

  // group_label_field/group_color_field only apply when group_by is a
  // relation field — its target resource comes from field metadata.
  const [groupByRelatedModel, setGroupByRelatedModel] = useState<string | null>(null);
  useEffect(() => {
    if (!view.group_label_field && !view.group_color_field) {
      setGroupByRelatedModel(null);
      return;
    }
    let cancelled = false;
    resourceMetadataRegistry
      .resolve(view.resource)
      .then((entry) => {
        if (cancelled) return;
        const field = entry?.fields.find((f) => f.name === view.group_by);
        setGroupByRelatedModel(field?.related_model ?? null);
      })
      .catch(() => {
        // Degrades to the raw group_by value as the column label.
        if (!cancelled) setGroupByRelatedModel(null);
      });
    return () => {
      cancelled = true;
    };
  }, [view.resource, view.group_by, view.group_label_field, view.group_color_field]);

  const groupIds = useMemo(
    () => deriveGroupIds(rows, view.group_by, view.group_values).filter((id) => id !== ""),
    [rows, view.group_by, view.group_values],
  );

  const relationSpecs: RelationBatchSpec[] = groupByRelatedModel
    ? [
        ...(view.group_label_field
          ? [{ key: "group-label", resource: groupByRelatedModel, labelField: view.group_label_field, ids: groupIds }]
          : []),
        ...(view.group_color_field
          ? [{ key: "group-color", resource: groupByRelatedModel, labelField: view.group_color_field, ids: groupIds }]
          : []),
      ]
    : [];
  const groupRelationLabels = useRelationLabels(relationSpecs);

  const dragField = view.drag_updates_field ?? view.group_by;
  const { queryKey } = createInfiniteListQueryOptions<Row>(view.resource, infiniteListOptions);

  const quickCreateMutation = useMutation({
    mutationFn: (payload: Record<string, unknown>) => saveRecord(view.resource, undefined, payload),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey });
    },
    onError: (err: unknown) => toast.error(err instanceof Error ? err.message : String(err)),
  });

  if (isLoading) {
    return <Skeleton type="card" />;
  }

  if (isError) {
    return (
      <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
        <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
        <p className="text-text">Couldn't load {view.label}.</p>
        {error && <p className="text-sm text-text-secondary">{error.message}</p>}
        <ActionButton
          variant="secondary"
          onClick={() => {
            void refetch();
          }}
        >
          Retry
        </ActionButton>
      </div>
    );
  }

  async function handleMoveCard(cardId: string, _fromGroupId: string, toGroupId: string): Promise<void> {
    await saveRecord(view.resource, cardId, { [dragField]: toGroupId });
    void queryClient.invalidateQueries({ queryKey });
  }

  // Sets group_by, not dragField — grouping is always keyed on group_by.
  function handleQuickCreate(groupId: string, values: Record<string, string>): void {
    quickCreateMutation.mutate({ ...values, [view.group_by]: groupId });
  }

  function handleLoadMore(groupId: string): void {
    const bucketLength = buckets.get(groupId)?.length ?? 0;
    const revealedCount = revealed[groupId] ?? maxPerColumn;
    setRevealed((prev) => ({ ...prev, [groupId]: revealedCount + maxPerColumn }));
    if (bucketLength <= revealedCount && hasNextPage && !isFetchingNextPage) void fetchNextPage();
  }

  function groupLabelOf(groupId: string): string {
    if (!view.group_label_field) return groupId;
    return groupRelationLabels.get("group-label")?.[groupId] ?? groupId;
  }

  function groupColorOf(groupId: string): string | undefined {
    if (!view.group_color_field) return undefined;
    return groupRelationLabels.get("group-color")?.[groupId];
  }

  const buckets = bucketRowsByGroup(rows, view.group_by);
  const { avatarField, titleField, secondaryFields } = splitCardFields(view.card_fields);
  // Suppressed the same as the header's ListActions "New" button: a
  // "create"-type entry navigates away, interrupting the parent form.
  const suppressCreateAction = embedded && !showCreateAction;
  const filterCreate = (actions: typeof view.card_actions) =>
    suppressCreateAction ? (actions ?? []).filter((action) => action.type !== "create") : (actions ?? []);
  const cardActions = filterCreate(view.card_actions);
  const columnActions = filterCreate(view.column_actions);
  const CustomCardComponent =
    view.card_component && componentRegistry.has(view.card_component)
      ? componentRegistry.resolve(view.card_component)
      : undefined;

  function buildCard(row: Row): KanbanCardData {
    const id = String(row.id ?? "");
    const titleValue = titleField ? row[titleField] : undefined;
    const title = titleValue !== undefined && titleValue !== null ? String(titleValue) : "";
    const secondary = secondaryFields
      .filter((field) => row[field] !== undefined && row[field] !== null)
      .map((field) => ({ key: field, value: String(row[field]) }));

    return {
      id,
      title,
      accessibleTitle: title,
      ...(secondary.length > 0 ? { secondaryFields: secondary } : {}),
      // No separate field names the avatar's owner, so it reuses the title.
      ...(avatarField ? { avatar: { name: title, avatarUrl: fileUrlOf(row[avatarField]) } } : {}),
      ...(cardActions.length > 0
        ? { actions: resolveKanbanActionItems(cardActions, module, navigate, routeAction, id) }
        : {}),
      ...(CustomCardComponent ? { render: () => <CustomCardComponent record={row} /> } : {}),
    };
  }

  const groups: KanbanGroup[] = groupIds.map((groupId) => {
    const bucket = buckets.get(groupId) ?? [];
    const revealedCount = revealed[groupId] ?? maxPerColumn;
    return {
      id: groupId,
      label: groupLabelOf(groupId),
      color: groupColorOf(groupId),
      cards: bucket.slice(0, revealedCount).map(buildCard),
      // hasNextPage also counts: one flat server cursor spans every column.
      hasMore: bucket.length > revealedCount || hasNextPage,
      ...(columnActions.length > 0
        ? { actions: resolveKanbanActionItems(columnActions, module, navigate, routeAction) }
        : {}),
    };
  });

  const viewActions = view.actions ?? [];

  return (
    <>
      <ListFilters filters={view.filters ?? []} values={listState.filter} onChange={listState.setFilter} />
      {viewActions.length > 0 && (
        <div className="flex items-center justify-between gap-2">
          <ListActions
            actions={viewActions}
            module={module}
            {...(embedded !== undefined ? { embedded } : {})}
            {...(showCreateAction !== undefined ? { showCreateAction } : {})}
          />
        </div>
      )}
      {groupIds.length === 0 ? (
        <EmptyState title={`No ${view.label.toLowerCase()} found.`} />
      ) : (
        <KanbanBoard
          groups={groups}
          onMoveCard={handleMoveCard}
          allowDrag={view.allow_drag ?? true}
          quickCreate={view.quick_create ?? false}
          {...(view.quick_create_fields
            ? { quickCreateFields: view.quick_create_fields.map((name) => ({ name, label: name })) }
            : {})}
          onQuickCreate={handleQuickCreate}
          onLoadMore={handleLoadMore}
        />
      )}
    </>
  );
}
