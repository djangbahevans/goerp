import { Checkbox, Icon, Skeleton } from "@goerp/sdk/components";
import { ChevronRight } from "lucide-react";
import type { KeyboardEvent, MouseEvent as ReactMouseEvent } from "react";
import { Fragment } from "react";
import { columnRendersOwnLink, renderCell } from "./column-renderers.js";
import type { ListColumn, Row } from "./list-view-types.js";
import type { SelectionHandle } from "./use-selection.js";
import type { TreeRow } from "./use-tree-rows.js";

// Deeper levels keep their aria-level but stop indenting, so a card stays readable at 360px.
const MAX_INDENT_DEPTH = 5;

function indentOf(depth: number): string {
  return `calc(var(--space-4) * ${Math.min(depth, MAX_INDENT_DEPTH)})`;
}

export function SelectAllCheckbox({
  label,
  ids,
  selectedIds,
  onToggle,
}: {
  label: string;
  ids: string[];
  selectedIds: ReadonlySet<string>;
  onToggle: () => void;
}) {
  const selectedCount = ids.filter((id) => selectedIds.has(id)).length;
  return (
    <Checkbox
      label={label}
      labelHidden
      checked={ids.length > 0 && selectedCount === ids.length}
      indeterminate={selectedCount > 0 && selectedCount < ids.length}
      onChange={onToggle}
    />
  );
}

function isEmptyValue(value: unknown): boolean {
  return value == null || value === "" || (Array.isArray(value) && value.length === 0);
}

// A custom or display_field column renders from more than its own field, so an empty field does not make it empty.
function isEmptyColumn(column: ListColumn, row: Row): boolean {
  return column.type !== "custom" && !column.display_field && isEmptyValue(row[column.field]);
}

// A tap on the card's own text navigates; a tap on a control or link inside it does that control's job.
function isInteractiveTarget(event: ReactMouseEvent<HTMLElement>): boolean {
  return (
    event.target instanceof Element &&
    event.target.closest("a, button, input, select, textarea, label, details, [role=button], [role=link]") !== null
  );
}

export interface CardGroup {
  key: string;
  rows: Row[];
}

export interface TreeCards {
  rows: ReadonlyMap<Row, TreeRow>;
  onToggle: (id: string) => void;
  onRetry: (id: string) => void;
}

interface ListCardsProps {
  label: string;
  columns: ListColumn[];
  groups: CardGroup[];
  groupBy: string | undefined;
  showSelection: boolean;
  selection: SelectionHandle;
  relationLabels: Map<string, Record<string, string>>;
  rowHref: (row: Row) => string | undefined;
  onOpenRow: (row: Row) => void;
  tree?: TreeCards;
}

// list-renderer.md "Cards below 768px": each row is a card with the primary column as its title and
// the other non-empty columns not marked `card: false` as label/value lines, in place of the table.
export function ListCards({
  label,
  columns,
  groups,
  groupBy,
  showSelection,
  selection,
  relationLabels,
  rowHref,
  onOpenRow,
  tree,
}: ListCardsProps) {
  const titleColumn = columns.find((column) => column.primary) ?? columns[0];
  const detailColumns = columns.filter((column) => column !== titleColumn && column.card !== false);

  const cellOf = (column: ListColumn, row: Row) => {
    const rawValue = row[column.field];
    const relationLabel = typeof rawValue === "string" ? relationLabels.get(column.field)?.[rawValue] : undefined;
    return renderCell(column, row, relationLabel !== undefined ? { relationLabel } : {});
  };

  return (
    <div>
      {groups.map((group) => {
        const selectableIds = group.rows.map((row) => row.id).filter((id): id is string => typeof id === "string");
        return (
          <div key={group.key}>
            {groupBy && (
              <h3 className="m-0 bg-bg-subtle p-3 text-left font-medium text-sm text-text-secondary">
                {groupBy} = {group.key}
              </h3>
            )}
            {showSelection && (
              <div className="flex items-center gap-3 border-border border-b bg-surface p-3 text-sm text-text-secondary">
                <SelectAllCheckbox
                  label={groupBy ? `Select all in ${group.key}` : `Select all ${label.toLowerCase()}`}
                  ids={selectableIds}
                  selectedIds={selection.selectedIds}
                  onToggle={() => selection.toggleAll(selectableIds)}
                />
                <span aria-hidden="true">Select all</span>
              </div>
            )}
            {/* biome-ignore lint/a11y/noRedundantRoles: Safari drops list semantics from a list-style: none list without it. */}
            <ul role="list" aria-label={groupBy ? `${groupBy} = ${group.key}` : label} className="m-0 list-none p-0">
              {group.rows.map((row, index) => {
                const id = typeof row.id === "string" ? row.id : undefined;
                const selected = id !== undefined && selection.selectedIds.has(id);
                const href = rowHref(row);
                const title = titleColumn ? cellOf(titleColumn, row) : null;
                const titleOwnsLink = titleColumn ? columnRendersOwnLink(titleColumn) : false;
                const treeRow = tree?.rows.get(row);
                const showChevron =
                  treeRow !== undefined && id !== undefined && (treeRow.hasChildrenUnknown || treeRow.hasChildren);
                const open = href ? () => onOpenRow(row) : undefined;
                const titleLinkable =
                  Boolean(href) && !titleOwnsLink && titleColumn !== undefined && !isEmptyValue(row[titleColumn.field]);
                return (
                  <Fragment key={id ?? index}>
                    {/* biome-ignore lint/a11y/useAriaPropsSupportedByRole: ARIA 1.2 lists aria-level among listitem's supported properties. */}
                    <li
                      aria-level={treeRow ? treeRow.depth + 1 : undefined}
                      style={
                        treeRow
                          ? { paddingInlineStart: `calc(var(--space-4) + ${indentOf(treeRow.depth)})` }
                          : undefined
                      }
                      className={`flex items-start gap-3 border-border border-b p-4 last:border-b-0 ${selected ? "bg-primary-subtle" : "bg-surface"} ${
                        open ? "cursor-pointer active:bg-surface-hover" : ""
                      }`}
                      tabIndex={open && !titleLinkable ? 0 : undefined}
                      onClick={
                        open
                          ? (event) => {
                              if (!isInteractiveTarget(event)) open();
                            }
                          : undefined
                      }
                      onKeyDown={
                        open && !titleLinkable
                          ? (event: KeyboardEvent<HTMLLIElement>) => {
                              if (event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " "))
                                return;
                              event.preventDefault();
                              open();
                            }
                          : undefined
                      }
                    >
                      {showSelection && id !== undefined && (
                        <Checkbox
                          label="Select row"
                          labelHidden
                          checked={selected}
                          onChange={() => selection.toggle(id)}
                        />
                      )}
                      <div className="flex min-w-0 flex-1 flex-col gap-2">
                        {titleColumn && (
                          <div className="flex items-start gap-2.5 font-medium text-base text-text [overflow-wrap:anywhere]">
                            {treeRow &&
                              (showChevron && id !== undefined ? (
                                <button
                                  type="button"
                                  aria-label={treeRow.isExpanded ? "Collapse" : "Expand"}
                                  aria-expanded={treeRow.isExpanded}
                                  onClick={() => tree?.onToggle(id)}
                                  className="-m-2.5 inline-flex size-11 shrink-0 items-center justify-center rounded-control"
                                >
                                  <ChevronRight
                                    size={16}
                                    aria-hidden="true"
                                    className={`transition-transform duration-(--duration-base) ease-out ${treeRow.isExpanded ? "rotate-90" : ""}`}
                                  />
                                </button>
                              ) : (
                                <span aria-hidden="true" className="inline-block size-6 shrink-0" />
                              ))}
                            <span className="min-w-0">
                              {titleLinkable && href ? (
                                <a
                                  href={href}
                                  onClick={(event: ReactMouseEvent<HTMLAnchorElement>) => {
                                    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey) return;
                                    event.preventDefault();
                                    onOpenRow(row);
                                  }}
                                >
                                  {title}
                                </a>
                              ) : (
                                title
                              )}
                            </span>
                          </div>
                        )}
                        <dl className="m-0 grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)] gap-x-3 gap-y-1">
                          {detailColumns
                            .filter((column) => !isEmptyColumn(column, row))
                            .map((column) => (
                              <div key={column.field} className="contents">
                                <dt className="text-sm text-text-secondary">{column.label ?? column.field}</dt>
                                <dd className="m-0 min-w-0 text-base text-text [overflow-wrap:anywhere]">
                                  {cellOf(column, row)}
                                </dd>
                              </div>
                            ))}
                        </dl>
                      </div>
                    </li>
                    {treeRow?.isLoadingChildren && (
                      <li
                        aria-busy="true"
                        className="border-border border-b bg-surface p-4"
                        style={{ paddingInlineStart: `calc(var(--space-4) + ${indentOf(treeRow.depth + 1)})` }}
                      >
                        <Skeleton lines={1} />
                      </li>
                    )}
                    {treeRow?.hasError && !treeRow.isLoadingChildren && id !== undefined && (
                      <li
                        className="flex flex-wrap items-center gap-2 border-border border-b bg-surface p-4 text-danger text-sm"
                        style={{ paddingInlineStart: `calc(var(--space-4) + ${indentOf(treeRow.depth + 1)})` }}
                      >
                        <Icon name="circle-alert" size={14} aria-hidden="true" />
                        Couldn't load these rows.
                        <button
                          type="button"
                          className="inline-flex min-h-11 items-center font-medium underline"
                          onClick={() => tree?.onRetry(id)}
                        >
                          Retry
                        </button>
                      </li>
                    )}
                  </Fragment>
                );
              })}
            </ul>
          </div>
        );
      })}
    </div>
  );
}
