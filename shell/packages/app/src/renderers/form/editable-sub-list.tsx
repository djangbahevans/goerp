import type { APIClient } from "@goerp/sdk";
import { apiClient } from "@goerp/sdk";
import { PermissionContext } from "@goerp/sdk/auth";
import {
  AlertDialog,
  Button,
  Checkbox,
  EmptyState,
  FieldControlProvider,
  Icon,
  IconButton,
  Skeleton,
  TagsField,
} from "@goerp/sdk/components";
import type { ActionRegistry, RelationBatchSpec } from "@goerp/sdk/react";
import { actionRegistry, createInfiniteListQueryOptions, useRelationLabels } from "@goerp/sdk/react";
import type { ModelRegistry, ResourceRegistry } from "@goerp/sdk/schema";
import { modelRegistry, resourceRegistry } from "@goerp/sdk/schema";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import type { KeyboardEvent, MouseEvent, ReactNode } from "react";
import { Fragment, useContext, useEffect, useId, useMemo, useRef, useState } from "react";
import { columnStyle, renderCell } from "../list/column-renderers.js";
import type { ListColumn, Row } from "../list/list-view-types.js";
import {
  type CellEditor,
  changedFields,
  editorFor,
  isEmptyValue,
  mapSaveError,
  resolveSubListRoutes,
  sendWrite,
  toSortParam,
} from "./editable-sub-list-model.js";
import { FieldInput } from "./field-renderers.js";
import type { FormSection } from "./form-view-types.js";
import { recordQueryKey } from "./use-form-record.js";

// docs/components/editable-sub-list.md — a `sub_list` section with
// `inline_edit: true`: rows that switch between view and edit in place,
// saved one at a time through the related model's own routes.

const PREVIEW_DEBOUNCE_MS = 300;
// Fits the edit-mode Save + Cancel pair (sm) plus cell padding.
const ACTIONS_COLUMN_WIDTH = 160;
const UNSIZED_COLUMN_MIN_WIDTH = 160;

export interface EditableSubListProps {
  section: FormSection;
  // The parent form's resource and its loaded record (inline_key rows
  // live on it, and its query is refreshed after every row write).
  parentResource: string;
  parentRecord: Row;
  recordId: string;
  target: { relatedModel: string; inverseField: string };
  columns: ListColumn[];
  label: string;
  formReadonly: boolean;
  // Rendered instead when nothing here is editable: a read-only form, or
  // a user who can neither create, update nor delete rows.
  readOnlyFallback: ReactNode;
  // Injection seams (stories, tests), the same ones useFormRecord takes.
  client?: Pick<APIClient, "get" | "post" | "put" | "patch" | "delete">;
  resources?: Pick<ResourceRegistry, "resolve">;
  models?: Pick<ModelRegistry, "resolve">;
  actions?: Pick<ActionRegistry, "resolve">;
}

interface EditState {
  key: string;
  isNew: boolean;
  base: Row;
  edits: Row;
  preview: Row;
  cellErrors: Record<string, string>;
  rowError: string | null;
}

type FocusTarget =
  | { kind: "cell"; key: string; field: string | null }
  | { kind: "edit"; key: string }
  | { kind: "add" }
  | { kind: "save" };

function rowKeyOf(row: Row): string | null {
  return typeof row.id === "string" ? row.id : null;
}

function draftValue(state: EditState, field: string): unknown {
  if (field in state.edits) return state.edits[field];
  if (field in state.preview) return state.preview[field];
  return state.base[field];
}

function isDirty(state: EditState): boolean {
  return Object.keys(changedFields(state.base, state.edits)).length > 0;
}

export function EditableSubList({
  section,
  parentResource,
  parentRecord,
  recordId,
  target,
  columns: declaredColumns,
  label,
  formReadonly,
  readOnlyFallback,
  client = apiClient,
  resources = resourceRegistry,
  models = modelRegistry,
  actions = actionRegistry,
}: EditableSubListProps): ReactNode {
  const { relatedModel, inverseField } = target;
  const permissions = useContext(PermissionContext);
  if (!permissions) {
    throw new Error("EditableSubList must be used within a PermissionProvider");
  }
  const queryClient = useQueryClient();
  const uid = useId();
  const inlineKey = section.inline_key;
  const cacheKeyPrefix = `embedded:${recordId}:${section.field ?? ""}-sub-list`;

  const meta = useQuery({
    queryKey: [
      "editable-sub-list-meta",
      relatedModel,
      section.create_route ?? null,
      section.update_route ?? null,
      section.delete_route ?? null,
    ],
    queryFn: async () => {
      const [entry, model] = await Promise.all([resources.resolve(relatedModel), models.resolve(relatedModel)]);
      const routes = await resolveSubListRoutes(section, entry, model, (name) => actions.resolve(name));
      return {
        routes,
        modelLabel: model.label,
        required: new Set(model.fields.filter((f) => f.required).map((f) => f.name)),
      };
    },
  });

  const list = useInfiniteQuery({
    ...createInfiniteListQueryOptions<Row>(
      relatedModel,
      {
        filter: { [inverseField]: recordId },
        ...(toSortParam(section.sort) !== undefined ? { sort: toSortParam(section.sort) as string } : {}),
        cacheKeyPrefix,
      },
      resources,
      client,
    ),
    enabled: inlineKey === undefined,
  });

  const inlineRows = inlineKey !== undefined ? parentRecord[inlineKey] : undefined;
  const baseRows = useMemo<Row[]>(
    () =>
      inlineKey !== undefined
        ? Array.isArray(inlineRows)
          ? (inlineRows as Row[])
          : []
        : (list.data?.pages.flatMap((page) => page.data) ?? []),
    [inlineKey, inlineRows, list.data],
  );

  // Saved values shown until the refetch they triggered lands, rows this
  // session created (kept even when they sort into an unloaded page), and
  // rows it deleted.
  const [overrides, setOverrides] = useState<Map<string, Row>>(() => new Map());
  const [created, setCreated] = useState<Row[]>([]);
  const [deleted, setDeleted] = useState<Set<string>>(() => new Set());
  // biome-ignore lint/correctness/useExhaustiveDependencies: a new baseRows identity is a fresh server snapshot, which supersedes local overrides.
  useEffect(() => {
    setOverrides(new Map());
  }, [baseRows]);

  const rows = useMemo(() => {
    const seen = new Set<string>();
    const out: Row[] = [];
    for (const row of baseRows) {
      const key = rowKeyOf(row);
      if (key !== null) {
        if (deleted.has(key)) continue;
        seen.add(key);
      }
      out.push(key !== null ? (overrides.get(key) ?? row) : row);
    }
    for (const row of created) {
      const key = rowKeyOf(row);
      if (key !== null && (seen.has(key) || deleted.has(key))) continue;
      out.push(key !== null ? (overrides.get(key) ?? row) : row);
    }
    return out;
  }, [baseRows, created, deleted, overrides]);

  const [editing, setEditingState] = useState<EditState | null>(null);
  const editingRef = useRef<EditState | null>(null);
  const setEditing = (next: EditState | null | ((prev: EditState | null) => EditState | null)) => {
    const value = typeof next === "function" ? next(editingRef.current) : next;
    editingRef.current = value;
    setEditingState(value);
  };
  const [saving, setSaving] = useState(false);
  const savingRef = useRef(false);
  const [confirmDelete, setConfirmDelete] = useState<Row | null>(null);
  const [deleteError, setDeleteError] = useState<{ key: string; message: string } | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const [focusTarget, setFocusTarget] = useState<FocusTarget | null>(null);
  const newRowCounter = useRef(0);
  // Only the latest preview request's answer is applied: an earlier one
  // can resolve after it and would otherwise overwrite newer values.
  const previewSeq = useRef(0);
  const scrollRef = useRef<HTMLDivElement>(null);
  const [hiddenRight, setHiddenRight] = useState(false);

  const columns = declaredColumns.filter((c) => permissions.checkField(relatedModel, c.field, "read"));
  const editors = new Map<string, CellEditor>();
  for (const column of columns) {
    const editor = editorFor(column);
    if (
      editor !== null &&
      !column.readonly &&
      column.field !== inverseField &&
      permissions.checkField(relatedModel, column.field, "write")
    ) {
      editors.set(column.field, editor);
    }
  }
  const labelOf = (field: string) => columns.find((c) => c.field === field)?.label ?? field;
  const primaryColumn = columns.find((c) => c.primary);

  const routes = meta.data?.routes;
  const holdsAll = (perms: string[] | undefined) => perms?.every((p) => permissions.check(p)) === true;
  const canCreate = holdsAll(routes?.create?.permissions);
  const canUpdate = holdsAll(routes?.update?.permissions);
  const canDelete = holdsAll(routes?.delete?.permissions);
  const modelLabel = meta.data?.modelLabel ?? label;

  // Moves focus once the render that mounts its target has committed.
  useEffect(() => {
    if (focusTarget === null) return;
    const id =
      focusTarget.kind === "cell"
        ? focusTarget.field === null
          ? firstEditableId(focusTarget.key)
          : cellId(focusTarget.key, focusTarget.field)
        : focusTarget.kind === "edit"
          ? `${uid}-edit-${focusTarget.key}`
          : focusTarget.kind === "add"
            ? `${uid}-add`
            : `${uid}-save`;
    const element = id === null ? null : document.getElementById(id);
    if (element) element.focus();
    else if (focusTarget.kind === "edit" || focusTarget.kind === "add") document.getElementById(`${uid}-add`)?.focus();
    setFocusTarget(null);
  });

  // Keeps the pinned actions column's leading edge while columns are
  // hidden to its right.
  // biome-ignore lint/correctness/useExhaustiveDependencies: re-measures whenever the rendered rows or edit state change the table's width.
  useEffect(() => {
    const el = scrollRef.current;
    if (el) setHiddenRight(el.scrollLeft + el.clientWidth < el.scrollWidth - 1);
  }, [rows, editing]);

  // editable-sub-list.md "Live recompute": post the draft to the related
  // model's preview action and merge its values into untouched fields.
  // biome-ignore lint/correctness/useExhaustiveDependencies: re-arms on each edit of the open row only.
  useEffect(() => {
    const previewPath = routes?.previewPath;
    const current = editingRef.current;
    if (!previewPath || !current || Object.keys(current.edits).length === 0) return;
    const timer = setTimeout(() => {
      previewSeq.current += 1;
      const seq = previewSeq.current;
      const draft = {
        ...current.base,
        ...current.edits,
        ...(current.isNew ? { [inverseField]: recordId } : {}),
      };
      client.post<Row>(previewPath, draft).then(
        (result) => {
          setEditing((prev) =>
            seq === previewSeq.current && prev && prev.key === current.key && result && typeof result === "object"
              ? { ...prev, preview: result }
              : prev,
          );
        },
        // Nothing was persisted; the next save recomputes server-side.
        () => {},
      );
    }, PREVIEW_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [editing?.key, editing?.edits, routes?.previewPath]);

  function cellId(key: string, field: string): string {
    return `${uid}-cell-${key}-${field}`;
  }

  function firstEditableId(key: string): string | null {
    const first = columns.find((c) => editors.has(c.field));
    return first ? cellId(key, first.field) : null;
  }

  const invalidateAfterWrite = () => {
    void queryClient.invalidateQueries({ queryKey: recordQueryKey(parentResource, recordId) });
    if (inlineKey === undefined) {
      void queryClient.invalidateQueries({ queryKey: ["infinite-list", cacheKeyPrefix] });
    }
  };

  const announce = (message: string) => {
    // Re-announces an identical message (two saves in a row).
    setAnnouncement((prev) => (prev === message ? `${message} ` : message));
  };

  // Resolves true once the open row is saved (or needed no save).
  async function saveEditing(): Promise<boolean> {
    const current = editingRef.current;
    if (!current || !routes) return true;
    if (savingRef.current) return false;
    const changes = changedFields(current.base, current.edits);
    if (!current.isNew && Object.keys(changes).length === 0) {
      setEditing(null);
      setFocusTarget({ kind: "edit", key: current.key });
      return true;
    }

    const required = meta.data?.required ?? new Set<string>();
    const missing: Record<string, string> = {};
    for (const field of editors.keys()) {
      if (!required.has(field) || (!current.isNew && !(field in changes))) continue;
      if (isEmptyValue(draftValue(current, field))) missing[field] = "Required.";
    }
    const missingFields = Object.keys(missing);
    if (missingFields.length > 0) {
      setEditing({ ...current, cellErrors: missing, rowError: null });
      setFocusTarget({ kind: "cell", key: current.key, field: firstInColumnOrder(missingFields) });
      return false;
    }

    const route = current.isNew ? routes.create : routes.update;
    if (!route) return false;
    savingRef.current = true;
    setSaving(true);
    try {
      const response = current.isNew
        ? await sendWrite<Row>(client, route, undefined, { ...changes, [inverseField]: recordId })
        : await sendWrite<Row>(client, route, current.key, changes);
      const returned = response && typeof response === "object" ? response : null;
      // A create with no usable body (a custom create_route answering 204)
      // has no id to address the row by — the refetch brings it in instead
      // of a local row keyed by the client-side "new-N" key.
      const saved: Row | null = current.isNew
        ? returned && rowKeyOf(returned) !== null
          ? returned
          : null
        : (returned ?? { ...current.base, ...changes });
      const savedKey = saved ? (rowKeyOf(saved) ?? current.key) : null;
      if (saved && savedKey !== null) {
        if (current.isNew) {
          setCreated((prev) => [...prev, saved]);
        } else {
          setOverrides((prev) => new Map(prev).set(savedKey, saved));
          // A row created this session is shown from `created` once a
          // refetch clears the overrides, so it carries the save too.
          setCreated((prev) => prev.map((row) => (rowKeyOf(row) === savedKey ? saved : row)));
        }
      }
      invalidateAfterWrite();
      announce(`${modelLabel} ${current.isNew ? "added" : "saved"}.`);

      // An edit made while the save was in flight stays unsaved.
      const latest = editingRef.current;
      const remaining: Row = {};
      if (saved && savedKey !== null && latest && latest.key === current.key) {
        for (const [field, value] of Object.entries(latest.edits)) {
          if (!(field in changes) || !Object.is(changes[field], value)) remaining[field] = value;
        }
      }
      if (saved && savedKey !== null && Object.keys(remaining).length > 0) {
        setEditing({
          key: savedKey,
          isNew: false,
          base: saved,
          edits: remaining,
          preview: {},
          cellErrors: {},
          rowError: null,
        });
      } else {
        setEditing(null);
        setFocusTarget(current.isNew || savedKey === null ? { kind: "add" } : { kind: "edit", key: savedKey });
      }
      return true;
    } catch (err) {
      const mapped = mapSaveError(err, new Set(editors.keys()), labelOf);
      setEditing((prev) =>
        prev && prev.key === current.key ? { ...prev, cellErrors: mapped.cells, rowError: mapped.row } : prev,
      );
      const invalid = Object.keys(mapped.cells);
      setFocusTarget(
        invalid.length > 0 ? { kind: "cell", key: current.key, field: firstInColumnOrder(invalid) } : { kind: "save" },
      );
      return false;
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  }

  function firstInColumnOrder(fields: string[]): string | null {
    return columns.find((c) => fields.includes(c.field) && editors.has(c.field))?.field ?? null;
  }

  // Leaves the open row before another one opens: saves it when it has
  // changes, and drops it (a blank new row included) when it doesn't.
  async function leaveEditing(): Promise<boolean> {
    const current = editingRef.current;
    if (!current) return true;
    if (!isDirty(current)) {
      setEditing(null);
      return true;
    }
    return saveEditing();
  }

  async function beginEdit(row: Row, field: string | null) {
    const key = rowKeyOf(row);
    if (key === null || !canUpdate) return;
    if (editingRef.current?.key === key) {
      setFocusTarget({ kind: "cell", key, field });
      return;
    }
    if (!(await leaveEditing())) return;
    setDeleteError(null);
    setEditing({ key, isNew: false, base: row, edits: {}, preview: {}, cellErrors: {}, rowError: null });
    setFocusTarget({ kind: "cell", key, field });
  }

  async function addRow() {
    if (!(await leaveEditing())) return;
    newRowCounter.current += 1;
    const key = `new-${newRowCounter.current}`;
    setEditing({ key, isNew: true, base: {}, edits: {}, preview: {}, cellErrors: {}, rowError: null });
    setFocusTarget({ kind: "cell", key, field: null });
  }

  function cancelEditing() {
    const current = editingRef.current;
    if (!current || savingRef.current) return;
    setEditing(null);
    if (isDirty(current)) announce("Changes discarded.");
    setFocusTarget(current.isNew ? { kind: "add" } : { kind: "edit", key: current.key });
  }

  function setValue(field: string, value: unknown) {
    setEditing((prev) => {
      if (!prev) return prev;
      const { [field]: _cleared, ...cellErrors } = prev.cellErrors;
      return { ...prev, edits: { ...prev.edits, [field]: value }, cellErrors };
    });
  }

  async function deleteRow(row: Row) {
    const key = rowKeyOf(row);
    const route = routes?.delete;
    if (key === null || !route) return;
    const index = rows.indexOf(row);
    try {
      await sendWrite(client, route, key);
      setDeleted((prev) => new Set(prev).add(key));
      setDeleteError(null);
      invalidateAfterWrite();
      announce(`${modelLabel} deleted.`);
      const neighbour = rows[index + 1] ?? rows[index - 1];
      const neighbourKey = neighbour ? rowKeyOf(neighbour) : null;
      setFocusTarget(neighbourKey !== null && canUpdate ? { kind: "edit", key: neighbourKey } : { kind: "add" });
    } catch (err) {
      setDeleteError({ key, message: mapSaveError(err, new Set(), labelOf).row ?? "Couldn't delete this row." });
    }
  }

  // Enter saves from a single-line input; Escape cancels. Both skip events
  // from portalled popovers (outside the row's DOM) and an open combobox.
  function onEditRowKeyDown(event: KeyboardEvent<HTMLTableRowElement>) {
    if (event.defaultPrevented) return;
    const targetEl = event.target as HTMLElement;
    if (!event.currentTarget.contains(targetEl)) return;
    if (targetEl.getAttribute("aria-expanded") === "true") return;
    if (event.key === "Escape") {
      event.preventDefault();
      cancelEditing();
    } else if (event.key === "Enter" && isSingleLineInput(targetEl)) {
      event.preventDefault();
      const current = editingRef.current;
      if (current && !current.isNew && !isDirty(current)) cancelEditing();
      else void saveEditing();
    }
  }

  const relationSpecs: RelationBatchSpec[] = columns
    .filter((c) => c.type === "relation" && !c.display_field && c.resource)
    .map((c) => ({
      key: c.field,
      resource: c.resource as string,
      ...(c.resource_label_field ? { labelField: c.resource_label_field } : {}),
      ids: rows.map((row) => row[c.field]).filter((value): value is string => typeof value === "string"),
    }));
  const relationLabels = useRelationLabels(relationSpecs);

  if (meta.isLoading || (inlineKey === undefined && list.isLoading)) {
    return <Skeleton type="table" columns={Math.max(columns.length, 1)} />;
  }
  if (meta.isError || (inlineKey === undefined && list.isError)) {
    const error = meta.error ?? list.error;
    return (
      <div className="rounded-structural border border-border bg-surface">
        <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
          <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
          <p className="text-text">Couldn't load {label}.</p>
          {error && <p className="text-sm text-text-secondary">{error.message}</p>}
          <Button
            variant="secondary"
            onClick={() => {
              if (meta.isError) void meta.refetch();
              if (list.isError) void list.refetch();
            }}
          >
            Retry
          </Button>
        </div>
      </div>
    );
  }
  if (formReadonly || !(canCreate || canUpdate || canDelete)) return readOnlyFallback;

  const hasActionsColumn = canUpdate || canDelete || editing !== null;
  // Declared widths, a floor for each undeclared one, and the actions
  // column: below this the surface scrolls rather than squeezing controls.
  const tableMinWidth =
    columns.reduce((sum, c) => sum + (c.width ?? c.min_width ?? UNSIZED_COLUMN_MIN_WIDTH), 0) +
    (hasActionsColumn ? ACTIONS_COLUMN_WIDTH : 0);
  const colCount = columns.length + (hasActionsColumn ? 1 : 0);
  const rowCount =
    (inlineKey === undefined ? (list.data?.pages[0]?.meta.total ?? rows.length) : rows.length) +
    (editing?.isNew ? 1 : 0);
  const atMax = section.max_rows !== undefined && rowCount >= section.max_rows;
  const addLabel = section.add_label ?? `Add ${modelLabel}`;
  const required = meta.data?.required ?? new Set<string>();
  const primaryName = (row: Row, index: number) => {
    const value = primaryColumn ? row[primaryColumn.field] : undefined;
    return isEmptyValue(value) ? `row ${index + 1}` : String(value);
  };

  const addButton = canCreate ? (
    <span className="flex items-center gap-2">
      <Button
        id={`${uid}-add`}
        variant="ghost"
        size="sm"
        icon="plus"
        disabled={atMax}
        aria-describedby={atMax ? `${uid}-max` : undefined}
        onClick={() => void addRow()}
      >
        {addLabel}
      </Button>
      {atMax && (
        <span id={`${uid}-max`} className="text-sm text-text-secondary">
          Up to {section.max_rows} {label.toLowerCase()}.
        </span>
      )}
    </span>
  ) : null;

  const liveRegion = (
    <span role="status" className="sr-only">
      {announcement}
    </span>
  );

  if (rows.length === 0 && !editing?.isNew) {
    return (
      <div className="rounded-structural border border-border bg-surface">
        <EmptyState title={`No ${label.toLowerCase()} yet`} {...(addButton ? { action: addButton } : {})} />
        {liveRegion}
      </div>
    );
  }

  const renderViewRow = (row: Row, index: number) => {
    const key = rowKeyOf(row);
    const name = primaryName(row, index);
    const editable = key !== null && canUpdate;
    const rowError = deleteError && deleteError.key === key ? deleteError.message : null;
    return (
      <Fragment key={key ?? `row-${index}`}>
        <tr className={`border-border border-b bg-surface ${editable ? "hover:bg-surface-hover" : ""}`}>
          {columns.map((column) => {
            const cellEditable = editable && editors.has(column.field);
            const rawValue = row[column.field];
            const relationLabel =
              typeof rawValue === "string" ? relationLabels.get(column.field)?.[rawValue] : undefined;
            return (
              // biome-ignore lint/a11y/useKeyWithClickEvents: a pointer shortcut only — the row's Edit button is the keyboard path to the same action (editable-sub-list.md "Row modes").
              <td
                key={column.field}
                style={columnStyle(column)}
                className={`p-3 text-base text-text ${cellEditable ? "cursor-text" : ""}`}
                onClick={
                  cellEditable
                    ? (event: MouseEvent) => {
                        if ((event.target as HTMLElement).closest("a")) return;
                        void beginEdit(row, column.field);
                      }
                    : undefined
                }
              >
                {renderCell({ ...column, href: undefined } as ListColumn, row, {
                  ...(relationLabel !== undefined ? { relationLabel } : {}),
                })}
              </td>
            );
          })}
          {hasActionsColumn && (
            <td className={actionsCellClassName(hiddenRight)}>
              {key !== null && (
                <span className="inline-flex items-center justify-end gap-1">
                  {canUpdate && (
                    <IconButton
                      id={`${uid}-edit-${key}`}
                      icon="pencil"
                      size="sm"
                      label={`Edit ${name}`}
                      onClick={() => void beginEdit(row, null)}
                    />
                  )}
                  {canDelete && (
                    <IconButton
                      icon="trash-2"
                      size="sm"
                      variant="danger"
                      label={`Delete ${name}`}
                      onClick={() => setConfirmDelete(row)}
                    />
                  )}
                </span>
              )}
            </td>
          )}
        </tr>
        {rowError !== null && <RowErrorLine colSpan={colCount} message={rowError} />}
      </Fragment>
    );
  };

  const renderEditRow = (state: EditState, row: Row | null, index: number) => {
    const name =
      row && !isEmptyValue(primaryColumn ? state.base[primaryColumn.field] : undefined)
        ? primaryName(row, index)
        : null;
    const displayRow: Row = { ...state.base, ...state.preview, ...state.edits };
    return (
      <Fragment key={state.key}>
        <tr className="border-border border-b bg-surface" onKeyDown={onEditRowKeyDown} aria-busy={saving || undefined}>
          {columns.map((column, columnIndex) => {
            const editor = editors.get(column.field);
            const inset = columnIndex === 0 ? "shadow-[inset_2px_0_0_var(--color-primary)]" : "";
            if (!editor) {
              const rawValue = displayRow[column.field];
              const relationLabel =
                typeof rawValue === "string" ? relationLabels.get(column.field)?.[rawValue] : undefined;
              return (
                // Top-aligned with p-3, a 20px line centres on the 36px
                // controls beside it (4px + 18px) even when an error grows
                // the row.
                <td
                  key={column.field}
                  style={columnStyle(column)}
                  className={`p-3 align-top text-base text-text ${inset}`}
                >
                  {renderCell({ ...column, href: undefined } as ListColumn, displayRow, {
                    ...(relationLabel !== undefined ? { relationLabel } : {}),
                  })}
                </td>
              );
            }
            const id = cellId(state.key, column.field);
            const error = state.cellErrors[column.field];
            const errorId = `${id}-error`;
            const controlLabel = name ? `${column.label ?? column.field}, ${name}` : (column.label ?? column.field);
            return (
              <td
                key={column.field}
                style={{ ...columnStyle(column), overflow: "visible", whiteSpace: "normal" }}
                className={`px-3 py-1 align-top ${inset}`}
              >
                <CellControl
                  id={id}
                  editor={editor}
                  column={column}
                  resource={relatedModel}
                  row={displayRow}
                  value={draftValue(state, column.field)}
                  label={controlLabel}
                  error={error}
                  errorId={errorId}
                  required={required.has(column.field)}
                  onChange={(value) => setValue(column.field, value)}
                />
              </td>
            );
          })}
          {/* py-2 centres the 28px sm buttons on the 36px controls. */}
          <td className={`${actionsCellClassName(hiddenRight)} py-2 align-top`}>
            <span className="inline-flex items-center justify-end gap-2">
              <Button
                id={`${uid}-save`}
                variant="primary"
                size="sm"
                loading={saving}
                disabled={!state.isNew && !isDirty(state) && !saving}
                onClick={() => void saveEditing()}
              >
                Save
              </Button>
              <Button variant="ghost" size="sm" onClick={cancelEditing}>
                Cancel
              </Button>
            </span>
          </td>
        </tr>
        {state.rowError !== null && <RowErrorLine colSpan={colCount} message={state.rowError} />}
      </Fragment>
    );
  };

  return (
    <div className="rounded-structural border border-border bg-surface">
      <div
        ref={scrollRef}
        className="overflow-x-auto"
        onScroll={(event) => {
          const el = event.currentTarget;
          setHiddenRight(el.scrollLeft + el.clientWidth < el.scrollWidth - 1);
        }}
      >
        {/* Fixed layout: column widths come from the header row, so a row
            switching to controls can't resize its columns (editable-sub-list.md
            "Cells in edit mode"). */}
        <table aria-label={label} className="w-full table-fixed border-collapse" style={{ minWidth: tableMinWidth }}>
          <thead>
            <tr className="border-border border-b bg-surface">
              {columns.map((column) => (
                <th
                  key={column.field}
                  scope="col"
                  style={columnStyle(column)}
                  className="p-3 text-left font-medium text-sm text-text-secondary"
                >
                  {column.label ?? column.field}
                  {editors.has(column.field) && required.has(column.field) && (
                    <span aria-hidden="true" className="text-danger">
                      {" "}
                      *
                    </span>
                  )}
                </th>
              ))}
              {hasActionsColumn && (
                <th
                  scope="col"
                  style={{ width: ACTIONS_COLUMN_WIDTH }}
                  className={`${actionsCellClassName(hiddenRight)} text-sm`}
                >
                  <span className="sr-only">Actions</span>
                </th>
              )}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, index) =>
              editing && !editing.isNew && editing.key === rowKeyOf(row)
                ? renderEditRow(editing, row, index)
                : renderViewRow(row, index),
            )}
            {editing?.isNew && renderEditRow(editing, null, rows.length)}
          </tbody>
        </table>
      </div>
      {inlineKey === undefined && list.hasNextPage && (
        <div className="flex justify-center border-border border-t p-3">
          <Button
            variant="secondary"
            loading={list.isFetchingNextPage}
            onClick={() => {
              void list.fetchNextPage();
            }}
          >
            Load more
          </Button>
        </div>
      )}
      {addButton && <div className="border-border border-t px-3 py-2">{addButton}</div>}
      {liveRegion}
      <AlertDialog
        open={confirmDelete !== null}
        title={`Delete this ${modelLabel.toLowerCase()}?`}
        description="This can't be undone."
        confirmLabel="Delete"
        confirmVariant="danger"
        tone="danger"
        onConfirm={() => {
          const row = confirmDelete;
          setConfirmDelete(null);
          if (row) void deleteRow(row);
        }}
        onCancel={() => setConfirmDelete(null)}
      />
    </div>
  );
}

// Fixed to the edit-mode Save + Cancel pair so the column doesn't resize
// when a row opens, pinned right so Save stays reachable on a wide row.
function actionsCellClassName(hiddenRight: boolean): string {
  return `sticky right-0 whitespace-nowrap bg-surface px-3 text-right ${hiddenRight ? "shadow-sm" : ""}`;
}

function isSingleLineInput(el: HTMLElement): boolean {
  if (el.tagName !== "INPUT" || el.getAttribute("role") === "combobox") return false;
  const type = (el as HTMLInputElement).type;
  return ["text", "number", "email", "tel", "url", "search"].includes(type);
}

function RowErrorLine({ colSpan, message }: { colSpan: number; message: string }): ReactNode {
  return (
    <tr className="border-border border-b">
      <td colSpan={colSpan} className="bg-danger-subtle px-3 py-2">
        <span role="alert" className="flex items-center gap-2 text-sm text-text">
          <Icon name="circle-alert" size={16} className="shrink-0 text-danger" aria-hidden="true" />
          {message}
        </span>
      </td>
    </tr>
  );
}

// One editable cell: a visually hidden label naming the control, and
// FieldWrapper's id/error/invalid wiring through FieldContext.
function CellControl({
  id,
  editor,
  column,
  resource,
  row,
  value,
  label,
  error,
  errorId,
  required,
  onChange,
}: {
  id: string;
  editor: CellEditor;
  column: ListColumn;
  resource: string;
  row: Row;
  value: unknown;
  label: string;
  error: string | undefined;
  errorId: string;
  required: boolean;
  onChange: (value: unknown) => void;
}): ReactNode {
  if (editor.kind === "checkbox") {
    return (
      // Column direction: Checkbox's own root is `self-start`, which would
      // override a row's align-items.
      <span className="flex h-9 flex-col justify-center">
        <Checkbox
          id={id}
          label={label}
          labelHidden
          checked={value === true}
          onChange={onChange}
          error={error}
          required={required || undefined}
        />
      </span>
    );
  }

  let control: ReactNode;
  if (editor.kind === "tags") {
    const tags = Array.isArray(value) ? value.map(String) : [];
    control = (
      <TagsField
        id={id}
        value={tags.map((tag) => ({ id: tag, name: tag }))}
        options={tags.map((tag) => ({ id: tag, name: tag }))}
        creatable
        onCreate={(name) => ({ id: name, name })}
        onChange={(next) => onChange(next.map((tag) => tag.name))}
        placeholder={`Add ${column.label ?? column.field}…`}
      />
    );
  } else {
    control = (
      <FieldInput field={editor.field} value={value} record={row} resource={resource} id={id} onChange={onChange} />
    );
  }

  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="sr-only">
        {label}
      </label>
      <FieldControlProvider
        value={{ id, describedBy: error !== undefined ? errorId : undefined, invalid: error !== undefined, required }}
      >
        {control}
      </FieldControlProvider>
      {error !== undefined && (
        <span id={errorId} className="text-danger text-sm">
          {error}
        </span>
      )}
    </div>
  );
}
