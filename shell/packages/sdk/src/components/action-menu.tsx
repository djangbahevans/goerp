import type { ReactNode } from "react";
import { useState } from "react";
import { useOptionalPermission } from "../auth/use-permission.js";

export interface ActionMenuItem {
  type?: "item" | "separator" | undefined;
  // Required for "item"; absent (and unused) for "separator".
  label?: string | undefined;
  icon?: string | undefined;
  onClick?: (() => void) | undefined;
  variant?: "default" | "danger" | undefined;
  permission?: string | undefined;
  disabled?: boolean | undefined;
}

export interface ActionMenuProps {
  label: string;
  items: ActionMenuItem[];
  disabled?: boolean | undefined;
}

function ActionMenuItemButton({ item, onSelect }: { item: ActionMenuItem; onSelect: () => void }): ReactNode {
  const allowed = useOptionalPermission(item.permission);
  if (!allowed) return null;

  return (
    <button
      type="button"
      role="menuitem"
      data-variant={item.variant ?? "default"}
      data-icon={item.icon}
      disabled={item.disabled}
      onClick={() => {
        item.onClick?.();
        onSelect();
      }}
    >
      {item.label}
    </button>
  );
}

export function ActionMenu({ label, items, disabled = false }: ActionMenuProps): ReactNode {
  const [open, setOpen] = useState(false);

  return (
    <span>
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={disabled}
        onClick={() => setOpen((v) => !v)}
      >
        {label}
      </button>
      {open && (
        <span role="menu">
          {items.map((item, index) => {
            // items is a static prop array with no unique identifier
            // field on ActionMenuItem (`label` is optional and callers
            // may repeat it, e.g. the same label gated by different
            // permissions) — index is the only stable key available.
            if (item.type === "separator") {
              // biome-ignore lint/suspicious/noArrayIndexKey: see above.
              return <hr key={index} />;
            }
            // biome-ignore lint/suspicious/noArrayIndexKey: see above.
            return <ActionMenuItemButton key={index} item={item} onSelect={() => setOpen(false)} />;
          })}
        </span>
      )}
    </span>
  );
}
