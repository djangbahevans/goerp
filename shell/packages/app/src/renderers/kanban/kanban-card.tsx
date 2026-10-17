import { UserAvatar } from "@goerp/sdk/components";
import { KanbanCardProvider } from "@goerp/sdk/react";
import type { DragEvent, KeyboardEvent, ReactNode, Ref } from "react";
import { KanbanActionsMenu } from "./kanban-actions-menu.js";
import type { KanbanCardData } from "./kanban-view-types.js";

export interface KanbanCardProps {
  card: KanbanCardData;
  isDragging: boolean;
  isDropTarget: boolean;
  columnId: string;
  tabIndex: number;
  onFocus: () => void;
  onPickUp: () => void;
  onDrop: () => void;
  onCancel: () => void;
  onMoveTarget: (direction: 1 | -1) => void;
  onDragStart: (event: DragEvent) => void;
  onDragEnd: () => void;
  // Card targets insert at a specific position; column targets append.
  onDragOverTarget: (event: DragEvent<HTMLDivElement>) => void;
  onDropOnTarget: (event: DragEvent<HTMLDivElement>) => void;
  allowDrag?: boolean | undefined;
  ref?: Ref<HTMLDivElement> | undefined;
}

function CardContent({ card }: { card: KanbanCardData }): ReactNode {
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-start justify-between gap-2">
        <span className="font-medium text-sm text-text">{card.title}</span>
        {card.actions && card.actions.length > 0 && <KanbanActionsMenu actions={card.actions} label="Card actions" />}
      </div>
      {card.avatar && (
        <UserAvatar name={card.avatar.name} avatarUrl={card.avatar.avatarUrl} userId={card.avatar.userId} size="sm" />
      )}
      {card.secondaryFields?.map((field) => (
        <div key={field.key} className="truncate text-text-secondary text-xs">
          {field.value}
        </div>
      ))}
    </div>
  );
}

export function KanbanCard({
  card,
  isDragging,
  isDropTarget,
  columnId,
  tabIndex,
  onFocus,
  onPickUp,
  onDrop,
  onCancel,
  onMoveTarget,
  onDragStart,
  onDragEnd,
  onDragOverTarget,
  onDropOnTarget,
  allowDrag = true,
  ref,
}: KanbanCardProps): ReactNode {
  function handleDragStart(event: DragEvent): void {
    // Prevent a custom handle's event from starting the same drag again on the wrapper.
    event.stopPropagation();
    if (!allowDrag) {
      event.preventDefault();
      return;
    }

    onDragStart(event);
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>): void {
    if (!allowDrag) return;
    if (event.key === " " || event.key === "Spacebar") {
      event.preventDefault();
      isDragging ? onDrop() : onPickUp();
      return;
    }
    if (!isDragging) return;
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      onMoveTarget(-1);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      onMoveTarget(1);
    } else if (event.key === "Escape") {
      event.preventDefault();
      onCancel();
    }
  }

  return (
    // biome-ignore lint/a11y/useSemanticElements: a draggable card, not a form's fieldset. It is a group rather than a button because it nests its own action buttons.
    <div
      ref={ref}
      role="group"
      draggable={allowDrag}
      tabIndex={tabIndex}
      onFocus={onFocus}
      onKeyDown={handleKeyDown}
      onDragStart={handleDragStart}
      onDragEnd={onDragEnd}
      onDragOver={allowDrag ? onDragOverTarget : undefined}
      onDrop={allowDrag ? onDropOnTarget : undefined}
      aria-roledescription="draggable kanban card"
      aria-grabbed={isDragging}
      aria-label={card.accessibleTitle}
      className={`kanban-card rounded-control border bg-surface p-3 text-left shadow-sm transition-shadow duration-(--duration-fast) ease-out focus-visible:shadow-focus focus-visible:outline-none ${
        isDragging ? "border-border border-dashed" : "border-border hover:border-border-strong hover:shadow-md"
      }`}
    >
      {card.render ? (
        <KanbanCardProvider
          isDragging={isDragging}
          isDropTarget={isDropTarget}
          columnId={columnId}
          onDragStart={handleDragStart}
          actions={card.actions ?? []}
        >
          {card.render({ isDragging })}
        </KanbanCardProvider>
      ) : (
        <div className={isDragging ? "opacity-50" : ""}>
          <CardContent card={card} />
        </div>
      )}
    </div>
  );
}
