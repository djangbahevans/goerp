// Presentational props — already-resolved groups/cards, not view-system.md
// §6's manifest fields, which is goerp#644's concern.

import type { ActionMenuItem } from "@goerp/sdk/components";
import type { ReactNode } from "react";

export interface KanbanCardField {
  key: string;
  value: ReactNode;
}

export interface KanbanCardAvatar {
  // Required by the real UserAvatar component, unlike the documented
  // CRMLeadCard sketch's <UserAvatar userId={...} /> (no name).
  name: string;
  avatarUrl?: string | null | undefined;
  userId?: string | undefined;
}

export interface KanbanCardData {
  id: string;
  title: ReactNode;
  // Plain-text form of `title`, since it may be arbitrary JSX — used for
  // the live-region pick-up/drop/cancel announcements.
  accessibleTitle: string;
  secondaryFields?: KanbanCardField[] | undefined;
  // The avatar_file_id-named field, rendered via UserAvatar regardless of
  // its position among card_fields (never folded into title/secondaryFields).
  avatar?: KanbanCardAvatar | undefined;
  actions?: ActionMenuItem[] | undefined;
  render?: ((props: { isDragging: boolean }) => ReactNode) | undefined;
}

export interface KanbanGroup {
  id: string;
  label: string;
  color?: string | undefined;
  cards: KanbanCardData[];
}

export interface KanbanQuickCreateField {
  name: string;
  label: string;
}

// Which card a mouse-dragged card is currently hovering over, and whether
// it would land before or after that card on drop.
export interface KanbanDropTarget {
  groupId: string;
  cardId: string;
  position: "before" | "after";
}

export interface KanbanBoardProps {
  groups: KanbanGroup[];
  onMoveCard: (cardId: string, fromGroupId: string, toGroupId: string) => Promise<void>;
  quickCreate?: boolean | undefined;
  quickCreateFields?: KanbanQuickCreateField[] | undefined;
  onQuickCreate?: ((groupId: string, values: Record<string, string>) => void) | undefined;
  emptyColumnMessage?: ((group: KanbanGroup) => string) | undefined;
}
