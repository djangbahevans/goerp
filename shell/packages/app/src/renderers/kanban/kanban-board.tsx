import type { DragEvent, ReactNode } from "react";
import { useState } from "react";
import { useOptimisticMutation } from "../shared/use-optimistic-mutation.js";
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
  allowDrag = true,
  onLoadMore,
}: KanbanBoardProps): ReactNode {
  const [prevGroupsProp, setPrevGroupsProp] = useState(groupsProp);
  const [groups, setGroups] = useState(groupsProp);
  const [pickedUp, setPickedUp] = useState<PickedUpCard | null>(null);
  const [mouseDraggingCardId, setMouseDraggingCardId] = useState<string | null>(null);
  const [dragOverGroupId, setDragOverGroupId] = useState<string | null>(null);
  const [dropTarget, setDropTarget] = useState<KanbanDropTarget | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const [focusedCardId, setFocusedCardId] = useState<string | null>(() => firstCardId(groupsProp));
  const runOptimisticMutation = useOptimisticMutation(setGroups);

  if (groupsProp !== prevGroupsProp) {
    setPrevGroupsProp(groupsProp);
    setGroups(groupsProp);
    // A deleted focus target must not leave every card at tabIndex -1.
    const focusedCardStillExists =
      focusedCardId !== null && groupsProp.some((group) => group.cards.some((card) => card.id === focusedCardId));
    if (!focusedCardStillExists) setFocusedCardId(firstCardId(groupsProp));
    // A concurrent refetch can invalidate the picked-up card's source column.
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
    await runOptimisticMutation({
      apply: (current) => reorderCards(current, cardId, fromGroupId, toGroupId, index),
      revert: (current) => reorderCards(current, cardId, toGroupId, fromGroupId, sourceIndex),
      commit: () => onMoveCard(cardId, fromGroupId, toGroupId),
      errorMessage: () => `Couldn't move "${cardTitle(groups, cardId)}". Please try again.`,
      logContext: "KanbanBoard: onMoveCard failed",
    });
  }

  function handlePickUp(cardId: string): void {
    if (!allowDrag) return;
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

  function handleCardDragStart(cardId: string, event: DragEvent): void {
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

  // Ignore bubbling dragleave events when the pointer stays inside the column.
  function handleColumnDragLeave(groupId: string, event: DragEvent<HTMLDivElement>): void {
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) return;
    setDragOverGroupId((current) => (current === groupId ? null : current));
  }

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
      <div className="flex gap-4 overflow-x-auto pb-2">
        {groups.map((group) => (
          <KanbanColumn
            key={group.id}
            group={group}
            allowDrag={allowDrag}
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
            onQuickCreateSubmit={(values) => onQuickCreate?.(group.id, values) ?? Promise.resolve()}
            hasMore={group.hasMore ?? false}
            onLoadMore={() => onLoadMore?.(group.id)}
          />
        ))}
      </div>
    </div>
  );
}
