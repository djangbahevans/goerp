import { useOptionalPermission } from "@goerp/sdk/auth";
import { ActionMenu, UserAvatar } from "@goerp/sdk/components";
import { KanbanCardProvider } from "@goerp/sdk/react";
import { MoreVertical } from "lucide-react";
import type { DragEvent, KeyboardEvent, ReactNode, Ref } from "react";
import type { KanbanCardData } from "./kanban-view-types.js";

export interface KanbanCardProps {
  card: KanbanCardData;
  // True whenever this card is the one currently being moved, whether via
  // native mouse drag or a keyboard pick-up — both present as "isDragging"
  // to the card and to any custom card_component via useKanbanCard().
  isDragging: boolean;
  tabIndex: number;
  onFocus: () => void;
  // Only meaningful when this card isn't already picked up.
  onPickUp: () => void;
  // Only meaningful while this card is picked up.
  onDrop: () => void;
  onCancel: () => void;
  onMoveTarget: (direction: 1 | -1) => void;
  onDragStart: (event: DragEvent<HTMLDivElement>) => void;
  onDragEnd: () => void;
  // Native mouse drag hovering/dropping on this card specifically (as
  // opposed to the column's general background) — drives intra-column
  // reordering and cross-column drops at a specific position.
  onDragOverTarget: (event: DragEvent<HTMLDivElement>) => void;
  onDropOnTarget: (event: DragEvent<HTMLDivElement>) => void;
  ref?: Ref<HTMLDivElement> | undefined;
}

function CardActions({ actions }: { actions: NonNullable<KanbanCardData["actions"]> }): ReactNode {
  // Called unconditionally (hooks can't be conditional) — ActionMenu
  // performs this same per-item permission check for the >1-action path
  // via useOptionalPermission inside ActionMenuItemButton; the lone-action
  // path must gate itself the same way rather than skipping it.
  const soleAction = actions.length === 1 ? actions[0] : undefined;
  const soleActionAllowed = useOptionalPermission(soleAction?.permission);

  if (actions.length === 0) return null;
  if (soleAction) {
    if (!soleActionAllowed) return null;
    return (
      <button
        type="button"
        onClick={soleAction.onClick}
        disabled={soleAction.disabled}
        title={soleAction.label}
        className="rounded-control px-1.5 py-0.5 text-text-secondary text-xs hover:bg-surface-hover hover:text-text focus-visible:outline-none focus-visible:shadow-focus"
      >
        {soleAction.label}
      </button>
    );
  }
  return (
    <ActionMenu
      label="Card actions"
      items={actions}
      trigger={({ ref, open, onClick, onKeyDown }) => (
        <button
          ref={ref}
          type="button"
          aria-haspopup="menu"
          aria-expanded={open}
          aria-label="Card actions"
          onClick={onClick}
          onKeyDown={onKeyDown}
          className="rounded-control p-1 text-text-secondary hover:bg-surface-hover hover:text-text focus-visible:outline-none focus-visible:shadow-focus"
        >
          <MoreVertical size={14} aria-hidden="true" />
        </button>
      )}
    />
  );
}

function CardContent({ card }: { card: KanbanCardData }): ReactNode {
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-start justify-between gap-2">
        <span className="font-medium text-sm text-text">{card.title}</span>
        {card.actions && card.actions.length > 0 && <CardActions actions={card.actions} />}
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
  ref,
}: KanbanCardProps): ReactNode {
  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>): void {
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
    // biome-ignore lint/a11y/useSemanticElements: must nest its own ActionMenu/lone-action <button> — a real <button> can't host an interactive descendant.
    <div
      ref={ref}
      role="button"
      draggable
      tabIndex={tabIndex}
      onFocus={onFocus}
      onKeyDown={handleKeyDown}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onDragOver={onDragOverTarget}
      onDrop={onDropOnTarget}
      aria-roledescription="draggable kanban card"
      aria-grabbed={isDragging}
      aria-label={card.accessibleTitle}
      className={`kanban-card rounded-control border bg-surface p-3 text-left shadow-sm transition-shadow duration-(--duration-fast) ease-out focus-visible:shadow-focus focus-visible:outline-none ${
        isDragging ? "border-dashed border-border" : "border-border hover:border-border-strong hover:shadow-md"
      }`}
    >
      {card.render ? (
        <KanbanCardProvider isDragging={isDragging}>{card.render({ isDragging })}</KanbanCardProvider>
      ) : (
        <div className={isDragging ? "opacity-50" : ""}>
          <CardContent card={card} />
        </div>
      )}
    </div>
  );
}
