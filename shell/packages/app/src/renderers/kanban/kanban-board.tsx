import { toast } from "@goerp/sdk/notifications";
import type { DragEvent, ReactNode } from "react";
import { useState } from "react";
import { KanbanColumn } from "./kanban-column.js";
import type { KanbanBoardProps, KanbanDropTarget, KanbanGroup } from "./kanban-view-types.js";

interface PickedUpCard {
  cardId: string;
  fromGroupId: string;
  targetGroupId: string;
}

const DEFAULT_QUICK_CREATE_FIELDS = [{ name: "title", label: "Title" }];

function findGroupIdForCard(groups: KanbanGroup[], cardId: string): string | undefined {
  return groups.find((group) => group.cards.some((card) => card.id === cardId))?.id;
}

function findCardIndex(groups: KanbanGroup[], groupId: string, cardId: string): number {
  return groups.find((group) => group.id === groupId)?.cards.findIndex((card) => card.id === cardId) ?? -1;
}

function firstCardId(groups: KanbanGroup[]): string | null {
  return groups.flatMap((group) => group.cards)[0]?.id ?? null;
}

// Removes the card from fromGroupId, inserts it into toGroupId at
// targetIndex. Order within a column is local-only — view-system.md §6's
// drag_updates_field only ever carries the group id, nothing to persist.
function reorderCards(
  groups: KanbanGroup[],
  cardId: string,
  fromGroupId: string,
  toGroupId: string,
  targetIndex: number,
): KanbanGroup[] {
  const fromGroup = groups.find((group) => group.id === fromGroupId);
  const card = fromGroup?.cards.find((c) => c.id === cardId);
  if (!card) return groups;
  return groups.map((group) => {
    if (group.id === fromGroupId && group.id === toGroupId) {
      const withoutCard = group.cards.filter((c) => c.id !== cardId);
      const index = Math.max(0, Math.min(targetIndex, withoutCard.length));
      return { ...group, cards: [...withoutCard.slice(0, index), card, ...withoutCard.slice(index)] };
    }
    if (group.id === fromGroupId) return { ...group, cards: group.cards.filter((c) => c.id !== cardId) };
    if (group.id === toGroupId) {
      const index = Math.max(0, Math.min(targetIndex, group.cards.length));
      return { ...group, cards: [...group.cards.slice(0, index), card, ...group.cards.slice(index)] };
    }
    return group;
  });
}

function cardTitle(groups: KanbanGroup[], cardId: string): string {
  for (const group of groups) {
    const card = group.cards.find((c) => c.id === cardId);
    if (card) return card.accessibleTitle;
  }
  return "Card";
}

function groupLabel(groups: KanbanGroup[], groupId: string): string {
  return groups.find((group) => group.id === groupId)?.label ?? groupId;
}

export function KanbanBoard({
  groups: groupsProp,
  onMoveCard,
  quickCreate = false,
  quickCreateFields = DEFAULT_QUICK_CREATE_FIELDS,
  onQuickCreate,
  emptyColumnMessage,
}: KanbanBoardProps): ReactNode {
  const [prevGroupsProp, setPrevGroupsProp] = useState(groupsProp);
  const [groups, setGroups] = useState(groupsProp);
  const [pickedUp, setPickedUp] = useState<PickedUpCard | null>(null);
  const [mouseDraggingCardId, setMouseDraggingCardId] = useState<string | null>(null);
  const [dragOverGroupId, setDragOverGroupId] = useState<string | null>(null);
  const [dropTarget, setDropTarget] = useState<KanbanDropTarget | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const [focusedCardId, setFocusedCardId] = useState<string | null>(() => firstCardId(groupsProp));

  // Reconciles with the caller's authoritative data (e.g. after a refetch)
  // during render rather than an effect — a plain if, not useEffect, since
  // this derives state from a prop change rather than syncing with
  // anything external.
  if (groupsProp !== prevGroupsProp) {
    setPrevGroupsProp(groupsProp);
    setGroups(groupsProp);
    // Re-derives whenever the focused card no longer exists in the new
    // props (deleted, or moved by someone else), not just when it was
    // never set — otherwise every card keeps tabIndex -1 and the board
    // becomes unreachable by Tab.
    const focusedCardStillExists =
      focusedCardId !== null && groupsProp.some((group) => group.cards.some((card) => card.id === focusedCardId));
    if (!focusedCardStillExists) setFocusedCardId(firstCardId(groupsProp));
    // Cancels a keyboard pick-up whose card no longer sits in its recorded
    // fromGroupId in the fresh data (e.g. a concurrent refetch) — dropping
    // it would otherwise pass onMoveCard a fromGroupId that's gone stale.
    if (pickedUp && findCardIndex(groupsProp, pickedUp.fromGroupId, pickedUp.cardId) === -1) setPickedUp(null);
  }

  const draggingCardId = pickedUp?.cardId ?? mouseDraggingCardId ?? null;

  async function moveCard(cardId: string, fromGroupId: string, toGroupId: string, targetIndex?: number): Promise<void> {
    const index = targetIndex ?? groups.find((group) => group.id === toGroupId)?.cards.length ?? 0;
    // A same-group move is a local-only reorder — nothing to call or revert.
    if (fromGroupId === toGroupId) {
      setGroups((current) => reorderCards(current, cardId, fromGroupId, toGroupId, index));
      return;
    }
    const sourceIndex = findCardIndex(groups, fromGroupId, cardId);
    setGroups((current) => reorderCards(current, cardId, fromGroupId, toGroupId, index));
    try {
      await onMoveCard(cardId, fromGroupId, toGroupId);
    } catch (error) {
      // Reverts against the *current* state, not the pre-move snapshot, so
      // an unrelated concurrent update survives the revert.
      setGroups((current) => reorderCards(current, cardId, toGroupId, fromGroupId, sourceIndex));
      toast.error(`Couldn't move "${cardTitle(groups, cardId)}". Please try again.`);
      console.error("KanbanBoard: onMoveCard failed", error);
    }
  }

  function handlePickUp(cardId: string): void {
    const fromGroupId = findGroupIdForCard(groups, cardId);
    if (!fromGroupId) return;
    setPickedUp({ cardId, fromGroupId, targetGroupId: fromGroupId });
    setAnnouncement(
      `${cardTitle(groups, cardId)} picked up. Use arrow keys to move between columns, Space to drop, Escape to cancel.`,
    );
  }

  function handleMoveTarget(direction: 1 | -1): void {
    if (!pickedUp) return;
    const order = groups.map((group) => group.id);
    const currentIndex = order.indexOf(pickedUp.targetGroupId);
    const nextIndex = currentIndex + direction;
    if (nextIndex < 0 || nextIndex >= order.length) return;
    const targetGroupId = order[nextIndex] as string;
    setPickedUp({ ...pickedUp, targetGroupId });
    setAnnouncement(`Moved to ${groupLabel(groups, targetGroupId)}. Press Space to drop, Escape to cancel.`);
  }

  function handleDrop(): void {
    if (!pickedUp) return;
    const { cardId, fromGroupId, targetGroupId } = pickedUp;
    setPickedUp(null);
    setAnnouncement(`${cardTitle(groups, cardId)} dropped in ${groupLabel(groups, targetGroupId)}.`);
    void moveCard(cardId, fromGroupId, targetGroupId);
  }

  function handleCancel(): void {
    if (!pickedUp) return;
    setAnnouncement(
      `Move cancelled. ${cardTitle(groups, pickedUp.cardId)} stays in ${groupLabel(groups, pickedUp.fromGroupId)}.`,
    );
    setPickedUp(null);
  }

  function handleCardDragStart(cardId: string, event: DragEvent<HTMLDivElement>): void {
    event.dataTransfer.setData("text/plain", cardId);
    event.dataTransfer.effectAllowed = "move";
    setMouseDraggingCardId(cardId);
  }

  function handleCardDragEnd(): void {
    setMouseDraggingCardId(null);
    setDragOverGroupId(null);
    setDropTarget(null);
  }

  function handleColumnDragOver(groupId: string, event: DragEvent<HTMLDivElement>): void {
    if (!mouseDraggingCardId) return;
    event.preventDefault();
    setDragOverGroupId(groupId);
  }

  // dragleave bubbles like mouseout — fires every time the pointer crosses
  // from the column background onto a nested card, not just when it truly
  // leaves the column. Ignore it unless relatedTarget is actually outside.
  function handleColumnDragLeave(groupId: string, event: DragEvent<HTMLDivElement>): void {
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) return;
    setDragOverGroupId((current) => (current === groupId ? null : current));
  }

  // Column background (not a specific card) — appends to the end.
  function handleColumnDrop(groupId: string, event: DragEvent<HTMLDivElement>): void {
    event.preventDefault();
    const cardId = event.dataTransfer.getData("text/plain");
    setDragOverGroupId(null);
    setDropTarget(null);
    setMouseDraggingCardId(null);
    const fromGroupId = findGroupIdForCard(groups, cardId);
    if (!fromGroupId || !cardId) return;
    void moveCard(cardId, fromGroupId, groupId);
  }

  // Dropping on a specific card: "before"/"after" comes from which half of
  // the target card the pointer is over.
  function handleCardDragOver(groupId: string, cardId: string, event: DragEvent<HTMLDivElement>): void {
    if (!mouseDraggingCardId || mouseDraggingCardId === cardId) return;
    event.preventDefault();
    event.stopPropagation();
    const rect = event.currentTarget.getBoundingClientRect();
    const position = event.clientY < rect.top + rect.height / 2 ? "before" : "after";
    setDragOverGroupId(groupId);
    setDropTarget({ groupId, cardId, position });
  }

  function handleCardDrop(groupId: string, cardId: string, event: DragEvent<HTMLDivElement>): void {
    event.preventDefault();
    event.stopPropagation();
    const draggedCardId = event.dataTransfer.getData("text/plain");
    const position = dropTarget?.position ?? "after";
    setDragOverGroupId(null);
    setDropTarget(null);
    setMouseDraggingCardId(null);
    const fromGroupId = findGroupIdForCard(groups, draggedCardId);
    if (!fromGroupId || !draggedCardId || draggedCardId === cardId) return;
    const targetCardIndex = findCardIndex(groups, groupId, cardId);
    if (targetCardIndex === -1) return;
    let targetIndex = position === "before" ? targetCardIndex : targetCardIndex + 1;
    // Removing the dragged card first shifts later indices left by one.
    if (fromGroupId === groupId) {
      const sourceIndex = findCardIndex(groups, groupId, draggedCardId);
      if (sourceIndex !== -1 && sourceIndex < targetIndex) targetIndex -= 1;
    }
    void moveCard(draggedCardId, fromGroupId, groupId, targetIndex);
  }

  return (
    <div className="flex flex-col gap-2">
      <span aria-live="polite" className="sr-only">
        {announcement}
      </span>
      {/* style={{ overflowX: "auto" }} instead of the `overflow-x-auto` utility: goerp#729 — that class generates no CSS in this build. */}
      <div className="flex gap-4 pb-2" style={{ overflowX: "auto" }}>
        {groups.map((group) => (
          <KanbanColumn
            key={group.id}
            group={group}
            draggingCardId={draggingCardId}
            isDropTarget={dragOverGroupId === group.id || pickedUp?.targetGroupId === group.id}
            onDragOver={(event) => handleColumnDragOver(group.id, event)}
            onDragLeave={(event) => handleColumnDragLeave(group.id, event)}
            onDrop={(event) => handleColumnDrop(group.id, event)}
            dropTarget={dropTarget?.groupId === group.id ? dropTarget : null}
            focusedCardId={focusedCardId}
            onFocusCard={setFocusedCardId}
            onPickUpCard={handlePickUp}
            onDropCard={handleDrop}
            onCancelCard={handleCancel}
            onMoveTarget={handleMoveTarget}
            onCardDragStart={handleCardDragStart}
            onCardDragEnd={handleCardDragEnd}
            onCardDragOver={(cardId, event) => handleCardDragOver(group.id, cardId, event)}
            onCardDrop={(cardId, event) => handleCardDrop(group.id, cardId, event)}
            emptyMessage={emptyColumnMessage?.(group) ?? "No cards in this column."}
            quickCreate={quickCreate}
            quickCreateFields={quickCreateFields}
            onQuickCreateSubmit={(values) => onQuickCreate?.(group.id, values)}
          />
        ))}
      </div>
    </div>
  );
}
