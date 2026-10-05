import { ActionMenu, type ActionMenuItem, IconButton } from "@goerp/sdk/components";
import type { ReactNode } from "react";

// An ActionMenu behind an icon-only trigger named by `label`, since
// ActionMenu's default trigger shows its label as visible text.
export function OverflowMenu({ label, items }: { label: string; items: ActionMenuItem[] }): ReactNode {
  return (
    <ActionMenu
      label={label}
      items={items}
      trigger={({ ref, open, disabled, onClick, onKeyDown }) => (
        <IconButton
          ref={ref}
          icon="ellipsis-vertical"
          label={label}
          size="sm"
          disabled={disabled}
          aria-haspopup="menu"
          aria-expanded={open}
          onClick={onClick}
          onKeyDown={onKeyDown}
        />
      )}
    />
  );
}
