import { Checkbox } from "@goerp/sdk/components";
import type { KeyboardEvent, MouseEvent as ReactMouseEvent } from "react";
import { columnRendersOwnLink, renderCell } from "./column-renderers.js";
import type { ListColumn, Row } from "./list-view-types.js";
import type { SelectionHandle } from "./use-selection.js";

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
}

// list-renderer.md "Cards below 768px": each row is a card with the primary column as its title and
// the other non-empty columns as label/value lines, in place of the table.
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
}: ListCardsProps) {
  const titleColumn = columns.find((column) => column.primary) ?? columns[0];
  const detailColumns = columns.filter((column) => column !== titleColumn);

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
                const open = href ? () => onOpenRow(row) : undefined;
                const titleLinkable =
                  Boolean(href) && !titleOwnsLink && titleColumn !== undefined && !isEmptyValue(row[titleColumn.field]);
                return (
                  <li
                    key={id ?? index}
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
                        <div className="font-medium text-base text-text [overflow-wrap:anywhere]">
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
                );
              })}
            </ul>
          </div>
        );
      })}
    </div>
  );
}
