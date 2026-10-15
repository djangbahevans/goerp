import { DndContext, KeyboardSensor, PointerSensor, useSensor, useSensors } from "@dnd-kit/core";
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import {
  Badge,
  Button,
  Checkbox,
  Combobox,
  FieldWrapper,
  Icon,
  PageHeader,
  PageLayout,
  TextInput,
} from "@goerp/sdk/components";
import { useOnline } from "@goerp/sdk/realtime";
import { type MetaSchema, useRefreshViewRegistry } from "@goerp/sdk/schema";
import { useEffect, useRef, useState } from "react";
import { type Draft, type EditableView, type ElementRow, editableViews, elementGroups } from "./view-elements.js";
import {
  resetOverrides,
  saveElement,
  useOverrideSchema,
  useViewOverrides,
  type ViewOverride,
} from "./view-overrides-api.js";

export function ViewOverridesPage() {
  const schema = useOverrideSchema();
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState("");
  const [dirty, setDirty] = useState(false);
  const views = schema.data ? editableViews(schema.data) : [];
  const view = views.find((candidate) => candidate.name === selected);
  const options = views.filter((candidate) =>
    `${candidate.name} ${candidate.module} ${candidate.type}`.toLowerCase().includes(search.toLowerCase()),
  );

  return (
    <PageLayout>
      <PageHeader title="View overrides" subtitle="Customise the fields and columns your tenant sees." />
      <div className="flex flex-col gap-6">
        <div className="max-w-lg">
          <FieldWrapper
            label="View"
            description={
              dirty
                ? "Save or reset your field changes before choosing another view."
                : "Search all list and form views by name."
            }
          >
            <Combobox
              query={search}
              onQueryChange={setSearch}
              options={options}
              getOptionKey={(option) => option.name}
              getOptionLabel={(option) => `${option.name} (${option.type})`}
              selectedLabel={view ? `${view.name} (${view.type})` : undefined}
              onSelect={(option) => setSelected(option.name)}
              disabled={dirty}
              status={schema.isPending ? "loading" : schema.isError ? "error" : "ready"}
              errorContent={<Button onClick={() => void schema.refetch()}>Retry loading views</Button>}
              emptyContent="No views match your search."
              placeholder="Find a view"
            />
          </FieldWrapper>
        </div>
        {view && schema.data ? (
          <ViewEditor
            key={view.name}
            view={view}
            schema={schema.data}
            reloadSchema={async () => {
              await schema.refetch({ throwOnError: true });
            }}
            onDirtyChange={setDirty}
          />
        ) : (
          !schema.isPending &&
          !schema.isError && <p className="text-text-secondary">Choose a view to edit its fields or columns.</p>
        )}
      </div>
    </PageLayout>
  );
}

function ViewEditor({
  view,
  schema,
  reloadSchema,
  onDirtyChange,
}: {
  view: EditableView;
  schema: MetaSchema;
  reloadSchema: () => Promise<void>;
  onDirtyChange: (dirty: boolean) => void;
}) {
  const overrides = useViewOverrides(view.name);
  const refreshRegistry = useRefreshViewRegistry();
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const online = useOnline();
  const [announcement, setAnnouncement] = useState("");
  const returnFocus = useRef<HTMLElement | null>(null);
  const groups = elementGroups(view, schema);
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  useEffect(() => {
    if (busy || !returnFocus.current) return;
    const target = returnFocus.current;
    returnFocus.current = null;
    if (!target.isConnected) return;
    const fallback = target.closest("fieldset")?.querySelector<HTMLElement>('button[aria-label^="Reorder "]');
    (target.matches(":disabled") ? fallback : target)?.focus();
  }, [busy]);

  function updateDraft(element: string, patch: Draft) {
    const next = { ...drafts, [element]: { ...drafts[element], ...patch } };
    setDrafts(next);
    onDirtyChange(true);
    setErrors((current) => ({ ...current, [element]: "" }));
    setAnnouncement("");
  }

  async function run(element: string, operation: () => Promise<void>, clear: string[], message: string) {
    if (busy) return;
    returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setBusy(true);
    onDirtyChange(true);
    setErrors((current) => ({ ...current, [element]: "" }));
    let succeeded = false;
    try {
      await operation();
      succeeded = true;
      setDrafts((current) => {
        const next = { ...current };
        for (const name of clear) delete next[name];
        return next;
      });
      setAnnouncement(message);
    } catch (error) {
      setErrors((current) => ({
        ...current,
        [element]: error instanceof Error ? error.message : "Couldn't save this change.",
      }));
      setAnnouncement("");
    } finally {
      // A multi-property operation can fail after earlier writes commit.
      // Reload both sources even on failure so badges and reset targets stay current.
      try {
        await Promise.all([overrides.refetch({ throwOnError: true }), reloadSchema()]);
        refreshRegistry();
      } catch {
        setErrors((current) => ({
          ...current,
          _view: succeeded
            ? "Changes saved, but the view could not be reloaded. Retry loading the view."
            : "Couldn't reload the view. Retry loading the view.",
        }));
      }
      setBusy(false);
      onDirtyChange(
        succeeded ? Object.keys(drafts).some((name) => !clear.includes(name)) : Object.keys(drafts).length > 0,
      );
    }
  }

  async function reorder(rows: ElementRow[], source: string, target: string) {
    const from = rows.findIndex((row) => row.key === source);
    const to = rows.findIndex((row) => row.key === target);
    if (from < 0 || to < 0 || from === to) return;
    const ordered = arrayMove(rows, from, to);
    await run(
      rows[from]?.element ?? "_view",
      async () => {
        for (const [index, row] of ordered.entries()) await saveElement(view.name, row.element, { order: index });
      },
      [],
      `${rows[from]?.element} moved to position ${to + 1}.`,
    );
  }

  if (overrides.isPending) return <p role="status">Loading view overrides…</p>;
  if (overrides.isError)
    return (
      <div role="alert">
        Couldn't load view overrides. <Button onClick={() => void overrides.refetch()}>Retry</Button>
      </div>
    );
  const saved = overrides.data ?? [];
  return (
    <div className="flex flex-col gap-4" aria-busy={busy}>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-text-secondary">
          Drag a handle to reorder. With the keyboard, press Space, use arrow keys, then press Space again.
        </p>
        <Button
          disabled={busy || !online || (saved.length === 0 && Object.keys(drafts).length === 0)}
          onClick={() =>
            void run("_view", () => resetOverrides(saved), Object.keys(drafts), "View reset to its defaults.")
          }
        >
          Reset all
        </Button>
      </div>
      {errors._view && (
        <div role="alert" className="text-danger">
          <p>{errors._view}</p>
          <Button disabled={busy} onClick={() => void run("_view", async () => {}, [], "View reloaded.")}>
            Retry loading the view
          </Button>
        </div>
      )}
      <p role="status" className="sr-only">
        {announcement}
      </p>
      {groups.length === 0 && <p className="text-text-secondary">This view has no editable fields or columns.</p>}
      {groups.map((group) => (
        <section key={group.key} className="min-w-0" aria-label={group.label}>
          <h2 className="mb-3 font-semibold text-lg text-text">{group.label}</h2>
          <DndContext
            sensors={sensors}
            onDragEnd={({ active, over }) => {
              if (over && !busy) void reorder(group.rows, String(active.id), String(over.id));
            }}
          >
            <SortableContext items={group.rows.map((row) => row.key)} strategy={verticalListSortingStrategy}>
              <div className="overflow-hidden rounded-structural border border-border bg-surface">
                <table className="w-full table-fixed border-collapse" aria-label={`${group.label} settings`}>
                  <thead className="sr-only">
                    <tr>
                      <th scope="col">Field settings</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {group.rows.map((row, index) => (
                      <ElementEditor
                        key={row.key}
                        row={row}
                        draft={drafts[row.element] ?? {}}
                        saved={saved.filter((override) => override.element === row.element)}
                        error={errors[row.element]}
                        disabled={busy || !online}
                        index={index}
                        count={group.rows.length}
                        onChange={(patch) => updateDraft(row.element, patch)}
                        onSave={() =>
                          void run(
                            row.element,
                            () => saveElement(view.name, row.element, drafts[row.element] ?? {}),
                            [row.element],
                            `${row.element} saved.`,
                          )
                        }
                        onReset={() =>
                          void run(
                            row.element,
                            () => resetOverrides(saved.filter((override) => override.element === row.element)),
                            [row.element],
                            `${row.element} reset to its default.`,
                          )
                        }
                        onMove={(offset) => {
                          const target = group.rows[index + offset];
                          if (target) void reorder(group.rows, row.key, target.key);
                        }}
                      />
                    ))}
                  </tbody>
                </table>
              </div>
            </SortableContext>
          </DndContext>
        </section>
      ))}
    </div>
  );
}

function ElementEditor({
  row,
  draft,
  saved,
  error,
  disabled,
  index,
  count,
  onChange,
  onSave,
  onReset,
  onMove,
}: {
  row: ElementRow;
  draft: Draft;
  saved: ViewOverride[];
  error: string | undefined;
  disabled: boolean;
  index: number;
  count: number;
  onChange: (patch: Draft) => void;
  onSave: () => void;
  onReset: () => void;
  onMove: (offset: number) => void;
}) {
  const { setNodeRef, setActivatorNodeRef, attributes, listeners, transform, transition, isDragging } = useSortable({
    id: row.key,
    disabled,
  });
  const persisted: Draft = {};
  for (const override of saved) persisted[override.property] = override.value;
  const value = { ...row.values, ...persisted, ...draft };
  const errorId = `${row.key}-error`;
  return (
    <tr
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition: transition }}
      className={`min-w-0 ${isDragging ? "relative z-10 bg-surface shadow-md" : ""}`}
    >
      <td className="p-4">
        <fieldset
          disabled={disabled}
          aria-label={row.element}
          aria-describedby={error ? errorId : undefined}
          className="flex min-w-0 flex-col gap-4"
        >
          <legend className="sr-only">{row.element}</legend>
          <div className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              ref={setActivatorNodeRef}
              {...attributes}
              {...listeners}
              aria-label={`Reorder ${row.element}`}
              className="touch-none rounded-control p-2 text-text-secondary focus-visible:outline-2 focus-visible:outline-primary max-md:min-h-11 max-md:min-w-11"
            >
              <Icon name="grip-vertical" size={18} aria-hidden="true" />
            </button>
            <span className="min-w-0 break-all font-medium text-text">{row.element}</span>
            {saved.length > 0 && <Badge label="Customised" color="blue" />}
            <span className="text-sm text-text-secondary">Position {index + 1}</span>
            <div className="ms-auto flex gap-2">
              <Button
                disabled={disabled || index === 0}
                onClick={() => onMove(-1)}
                aria-label={`Move ${row.element} up`}
              >
                ↑
              </Button>
              <Button
                disabled={disabled || index === count - 1}
                onClick={() => onMove(1)}
                aria-label={`Move ${row.element} down`}
              >
                ↓
              </Button>
            </div>
          </div>
          <div className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-3">
            <FieldWrapper label="Label">
              <TextInput value={String(value.label)} onChange={(label) => onChange({ label })} />
            </FieldWrapper>
            <FieldWrapper label="Help text">
              <TextInput value={String(value.help_text)} onChange={(help_text) => onChange({ help_text })} />
            </FieldWrapper>
            <FieldWrapper label="Placeholder">
              <TextInput value={String(value.placeholder)} onChange={(placeholder) => onChange({ placeholder })} />
            </FieldWrapper>
          </div>
          <div className="flex flex-wrap items-start gap-4">
            <div className="max-w-lg">
              <Checkbox
                label="Hidden"
                checked={value.hidden === true}
                onChange={(hidden) => onChange({ hidden })}
                description="Hiding a field does not restrict API access. Only the field's access declaration does."
              />
            </div>
            {row.requiredControl && (
              <Checkbox
                label="Required"
                checked={value.required === true}
                onChange={(required) => onChange({ required })}
              />
            )}
            <div className="ms-auto flex flex-wrap gap-2">
              <Button disabled={disabled || (saved.length === 0 && Object.keys(draft).length === 0)} onClick={onReset}>
                Reset to default
              </Button>
              <Button variant="primary" disabled={disabled || Object.keys(draft).length === 0} onClick={onSave}>
                Save field
              </Button>
            </div>
          </div>
          {error && (
            <p role="alert" id={errorId} className="text-danger text-sm">
              {error}
            </p>
          )}
        </fieldset>
      </td>
    </tr>
  );
}
