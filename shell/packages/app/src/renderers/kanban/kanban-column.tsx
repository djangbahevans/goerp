import { EmptyState } from "@goerp/sdk/components";
import type { DragEvent, ReactNode } from "react";
import { Fragment } from "react";
import { KanbanActionsMenu } from "./kanban-actions-menu.js";
import { KanbanCard } from "./kanban-card.js";
import { KanbanQuickCreateRow } from "./kanban-quick-create-row.js";
import type { KanbanDropTarget, KanbanGroup, KanbanQuickCreateField } from "./kanban-view-types.js";

export interface KanbanColumnProps {
  group: KanbanGroup;
  allowDrag: boolean;
  // The card currently being moved (mouse drag or keyboard pick-up), if any.
  draggingCardId: string | null;
  // True while a dragged/picked-up card is currently targeting this column.
  isDropTarget: boolean;
  onDragOver: (event: DragEvent<HTMLDivElement>) => void;
  onDragLeave: (event: DragEvent<HTMLDivElement>) => void;
  onDrop: (event: DragEvent<HTMLDivElement>) => void;
  // Which card in this column a drag is hovering over, if any — renders a
  // thin insertion-line indicator before/after that card.
  dropTarget: KanbanDropTarget | null;
  focusedCardId: string | null;
  onFocusCard: (cardId: string) => void;
  onPickUpCard: (cardId: string) => void;
  onDropCard: () => void;
  onCancelCard: () => void;
  onMoveTarget: (direction: 1 | -1) => void;
  onCardDragStart: (cardId: string, event: DragEvent<HTMLDivElement>) => void;
  onCardDragEnd: () => void;
  onCardDragOver: (cardId: string, event: DragEvent<HTMLDivElement>) => void;
  onCardDrop: (cardId: string, event: DragEvent<HTMLDivElement>) => void;
  emptyMessage: string;
  quickCreate: boolean;
  quickCreateFields: KanbanQuickCreateField[];
  onQuickCreateSubmit: (values: Record<string, string>) => void;
  hasMore: boolean;
  onLoadMore: () => void;
}

// Thin insertion-line marker for where a dragged card would land.
function DropIndicator(): ReactNode {
  return <div aria-hidden="true" className="h-0.5 rounded-full bg-primary" />;
}

export function KanbanColumn({
  group,
  allowDrag,
  draggingCardId,
  isDropTarget,
  onDragOver,
  onDragLeave,
  onDrop,
  dropTarget,
  focusedCardId,
  onFocusCard,
  onPickUpCard,
  onDropCard,
  onCancelCard,
  onMoveTarget,
  onCardDragStart,
  onCardDragEnd,
  onCardDragOver,
  onCardDrop,
  emptyMessage,
  quickCreate,
  quickCreateFields,
  onQuickCreateSubmit,
  hasMore,
  onLoadMore,
}: KanbanColumnProps): ReactNode {
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: mouse-drop target only — the keyboard-operable equivalent is entirely the card's own Space/Arrow/Escape handling, not this container.
    <div
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
      className={`flex w-72 flex-none flex-col gap-2 rounded-structural border p-2 transition-colors duration-(--duration-fast) ease-out ${
        isDropTarget ? "border-primary border-dashed bg-primary-subtle" : "border-transparent bg-bg-subtle"
      }`}
    >
      <div className="flex items-center gap-2 px-1 py-1">
        {group.color && (
          <span
            aria-hidden="true"
            className="h-2 w-2 flex-none rounded-full"
            style={{ backgroundColor: group.color }}
          />
        )}
        <h3 className="truncate font-medium text-sm text-text">{group.label}</h3>
        <span
          className={
            group.actions && group.actions.length > 0
              ? "text-text-secondary text-xs"
              : "ml-auto text-text-secondary text-xs"
          }
        >
          {group.cards.length}
        </span>
        {group.actions && group.actions.length > 0 && (
          <span className="ml-auto">
            <KanbanActionsMenu actions={group.actions} label="Column actions" />
          </span>
        )}
      </div>

      {/* overflow-x explicit "hidden": the CSS spec force-computes a "visible" x-axis to "auto" next to overflow-y-auto, which turned an open card's ActionMenu into an unwanted horizontal scrollbar. */}
      <div className="flex max-h-full flex-col gap-2 overflow-x-hidden overflow-y-auto">
        {group.cards.length === 0 ? (
          <EmptyState title={emptyMessage} size="compact" />
        ) : (
          group.cards.map((card) => (
            <Fragment key={card.id}>
              {dropTarget?.cardId === card.id && dropTarget.position === "before" && <DropIndicator />}
              <KanbanCard
                card={card}
                allowDrag={allowDrag}
                isDragging={draggingCardId === card.id}
                tabIndex={focusedCardId === card.id ? 0 : -1}
                onFocus={() => onFocusCard(card.id)}
                onPickUp={() => onPickUpCard(card.id)}
                onDrop={onDropCard}
                onCancel={onCancelCard}
                onMoveTarget={onMoveTarget}
                onDragStart={(event) => onCardDragStart(card.id, event)}
                onDragEnd={onCardDragEnd}
                onDragOverTarget={(event) => onCardDragOver(card.id, event)}
                onDropOnTarget={(event) => onCardDrop(card.id, event)}
              />
              {dropTarget?.cardId === card.id && dropTarget.position === "after" && <DropIndicator />}
            </Fragment>
          ))
        )}
      </div>

      {hasMore && (
        <button
          type="button"
          onClick={onLoadMore}
          className="w-full rounded-control p-2 text-center text-text-secondary text-xs hover:bg-surface-hover hover:text-text focus-visible:outline-none focus-visible:shadow-focus"
        >
          Load more
        </button>
      )}

      {quickCreate && (
        <KanbanQuickCreateRow groupId={group.id} fields={quickCreateFields} onSubmit={onQuickCreateSubmit} />
      )}
    </div>
  );
}
