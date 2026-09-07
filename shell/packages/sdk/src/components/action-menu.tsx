import type { ReactNode } from "react";
import { useState } from "react";
import { useOptionalPermission } from "../auth/use-permission.js";
import { actionButtonClassName } from "./action-button-styles.js";

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

const ITEM_CLASSES =
  "flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-text hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-50 data-[variant=danger]:text-danger data-[variant=danger]:hover:text-danger-hover";

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
      aria-disabled={item.disabled}
      className={ITEM_CLASSES}
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
    <span className="relative inline-block">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={disabled}
        className={actionButtonClassName("secondary", "md")}
        onClick={() => setOpen((v) => !v)}
      >
        {label}
      </button>
      {open && (
        <span
          role="menu"
          className="absolute z-(--z-dropdown) mt-1 min-w-40 max-w-70 rounded-structural border border-border bg-surface py-1 shadow-md"
        >
          {items.map((item, index) => {
            // items is a static prop array with no unique identifier
            // field on ActionMenuItem (`label` is optional and callers
            // may repeat it, e.g. the same label gated by different
            // permissions) — index is the only stable key available.
            if (item.type === "separator") {
              // biome-ignore lint/suspicious/noArrayIndexKey: see above.
              return <hr key={index} className="mx-2 my-1 border-t border-border" />;
            }
            // biome-ignore lint/suspicious/noArrayIndexKey: see above.
            return <ActionMenuItemButton key={index} item={item} onSelect={() => setOpen(false)} />;
          })}
        </span>
      )}
    </span>
  );
}
