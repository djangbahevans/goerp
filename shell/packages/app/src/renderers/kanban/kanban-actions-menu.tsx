import { useOptionalPermission } from "@goerp/sdk/auth";
import { ActionMenu, type ActionMenuItem, IconButton } from "@goerp/sdk/components";
import type { ReactNode } from "react";

export interface KanbanActionsMenuProps {
  actions: ActionMenuItem[];
  // Accessible name for the overflow trigger and the ActionMenu itself —
  // "Card actions" / "Column actions" depending on caller.
  label: string;
}

// Shared by kanban-card.tsx and kanban-column.tsx's own overflow menus.
export function KanbanActionsMenu({ actions, label }: KanbanActionsMenuProps): ReactNode {
  // Called unconditionally (hooks can't be conditional) — ActionMenu
  // performs this same per-item permission check for the >1-action path
  // via useOptionalPermission inside ActionMenuItemButton; the lone-action
  // path must gate itself the same way rather than skipping it. A
  // confirm-gated sole action skips this compact-button shortcut entirely
  // and falls through to a real (1-item) ActionMenu below, which already
  // owns the confirm-dialog flow — not worth re-implementing here too.
  const soleAction = actions.length === 1 && !actions[0]?.confirm ? actions[0] : undefined;
  const soleActionAllowed = useOptionalPermission(soleAction?.permission);

  if (actions.length === 0) return null;
  if (soleAction) {
    if (!soleActionAllowed) return null;
    return (
      <button
        type="button"
        onClick={() => soleAction.onClick?.()}
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
      label={label}
      items={actions}
      trigger={({ ref, open, onClick, onKeyDown }) => (
        <IconButton
          ref={ref}
          icon="ellipsis-vertical"
          label={label}
          size="sm"
          aria-haspopup="menu"
          aria-expanded={open}
          onClick={onClick}
          onKeyDown={onKeyDown}
        />
      )}
    />
  );
}
