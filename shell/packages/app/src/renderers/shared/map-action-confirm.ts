import type { ActionMenuItemConfirm } from "@goerp/sdk/components";
import type { ListAction } from "../list/list-view-types.js";

// Shared by list-actions.tsx (header/row actions) and kanban-actions.tsx
// (card/column actions) — both resolve a manifest ListAction into an
// ActionMenuItem and need the same snake_case-to-camelCase translation
// for its optional `confirm` field.
export function mapActionConfirm(confirm: ListAction["confirm"]): ActionMenuItemConfirm | undefined {
  if (!confirm) return undefined;
  return {
    title: confirm.title,
    message: confirm.message,
    confirmLabel: confirm.confirm_label,
    cancelLabel: confirm.cancel_label,
    destructive: confirm.destructive,
    input: confirm.input,
  };
}
