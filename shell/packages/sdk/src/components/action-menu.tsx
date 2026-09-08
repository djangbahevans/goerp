import type { KeyboardEvent, ReactNode } from "react";
import { useEffect, useRef, useState } from "react";
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
  "flex w-full items-center gap-2 truncate px-3 py-2 text-left text-sm text-text hover:bg-surface-hover focus:bg-surface-hover focus:outline-none aria-disabled:cursor-not-allowed aria-disabled:opacity-50 aria-disabled:hover:bg-transparent aria-disabled:focus:bg-transparent data-[variant=danger]:text-danger data-[variant=danger]:hover:text-danger-hover data-[variant=danger]:focus:text-danger-hover";

// Indices into `refs` with a real, focusable menuitem — separators and
// permission-denied items never populate their slot.
function focusableIndices(refs: readonly (HTMLButtonElement | null)[]): number[] {
  return refs.flatMap((ref, index) => (ref ? [index] : []));
}

// Steps from the currently DOM-focused item (not stored render state, which
// could go stale if `items` changes shape while the menu is open) to the
// next focusable item, wrapping around.
function nextFocusableIndex(refs: readonly (HTMLButtonElement | null)[], direction: 1 | -1): number {
  const focusable = focusableIndices(refs);
  if (focusable.length === 0) return 0;
  // instanceof-guarded rather than a bare `refs.indexOf(document.activeElement)`:
  // a null activeElement would otherwise false-positive-match any ref slot a
  // just-unmounted item left explicitly null.
  const focusedRefIndex =
    document.activeElement instanceof HTMLButtonElement ? refs.indexOf(document.activeElement) : -1;
  const at = focusable.indexOf(focusedRefIndex);
  // No item currently focused: land on the first for ArrowDown, last for ArrowUp.
  const from = at === -1 ? (direction === 1 ? -1 : focusable.length) : at;
  const nextAt = (from + direction + focusable.length) % focusable.length;
  return focusable[nextAt] as number;
}

function ActionMenuItemButton({
  item,
  tabIndex,
  itemRef,
  onSelect,
}: {
  item: ActionMenuItem;
  tabIndex: number;
  itemRef: (el: HTMLButtonElement | null) => void;
  onSelect: () => void;
}): ReactNode {
  const allowed = useOptionalPermission(item.permission);
  if (!allowed) return null;

  return (
    <button
      ref={itemRef}
      type="button"
      role="menuitem"
      tabIndex={tabIndex}
      data-variant={item.variant ?? "default"}
      data-icon={item.icon}
      // Not the native `disabled` attribute — that would remove the item
      // from focus entirely, contradicting the ARIA APG's disabled-menuitem
      // convention this component follows: reachable by keyboard, just
      // inert on activation.
      aria-disabled={item.disabled}
      title={item.label}
      className={ITEM_CLASSES}
      onClick={() => {
        if (item.disabled) return;
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
  const [activeIndex, setActiveIndex] = useState(0);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const itemRefs = useRef<(HTMLButtonElement | null)[]>([]);
  // One stable callback per index, so a re-render (e.g. every arrow-key
  // press) doesn't churn every item's ref via a fresh inline closure.
  const itemRefCallbacks = useRef(new Map<number, (el: HTMLButtonElement | null) => void>());

  function getItemRefCallback(index: number): (el: HTMLButtonElement | null) => void {
    let callback = itemRefCallbacks.current.get(index);
    if (!callback) {
      callback = (el) => {
        itemRefs.current[index] = el;
      };
      itemRefCallbacks.current.set(index, callback);
    }
    return callback;
  }

  // Which end to focus once the menu's items exist — ArrowUp on the closed
  // trigger opens straight to the last item, matching the ARIA APG
  // menu-button pattern (docs/components/action-menu.md's Accessibility
  // section); every other opening path (click, ArrowDown) focuses the first.
  const openFocusDirection = useRef<1 | -1>(1);

  useEffect(() => {
    if (!open) return;
    const first = nextFocusableIndex(itemRefs.current, openFocusDirection.current);
    setActiveIndex(first);
    itemRefs.current[first]?.focus();
  }, [open]);

  const move = (direction: 1 | -1) => {
    const next = nextFocusableIndex(itemRefs.current, direction);
    setActiveIndex(next);
    itemRefs.current[next]?.focus();
  };

  const openMenu = (direction: 1 | -1) => {
    openFocusDirection.current = direction;
    setOpen(true);
  };

  const handleTriggerKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      openMenu(1);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      openMenu(-1);
    }
  };

  const handleMenuKeyDown = (event: KeyboardEvent<HTMLSpanElement>) => {
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        move(1);
        break;
      case "ArrowUp":
        event.preventDefault();
        move(-1);
        break;
      case "Escape":
        event.preventDefault();
        // Stops here rather than also closing an ancestor dialog/popover
        // that has its own Escape-to-close listener — only the innermost
        // open layer should respond to one Escape press.
        event.stopPropagation();
        setOpen(false);
        triggerRef.current?.focus();
        break;
      case "Tab":
        // No preventDefault, and no synchronous setOpen here: the roving
        // tabindex leaves only one item in the native Tab order, so a
        // single Tab press must still let the browser's own default action
        // move focus to whatever's next on the page. Closing synchronously
        // would unmount that focused item out from under the browser mid
        // default-action; deferring a tick lets Tab land first, then closes
        // the now-unfocused menu instead of leaving it stuck open.
        setTimeout(() => setOpen(false), 0);
        break;
      default:
        break;
    }
  };

  return (
    <span className="relative inline-block">
      <button
        ref={triggerRef}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        data-disabled={disabled ? "true" : undefined}
        disabled={disabled}
        className={actionButtonClassName("secondary", "md")}
        onClick={() => (open ? setOpen(false) : openMenu(1))}
        onKeyDown={handleTriggerKeyDown}
      >
        {label}
      </button>
      {open && (
        <span
          role="menu"
          onKeyDown={handleMenuKeyDown}
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
            return (
              <ActionMenuItemButton
                // biome-ignore lint/suspicious/noArrayIndexKey: see above.
                key={index}
                item={item}
                tabIndex={index === activeIndex ? 0 : -1}
                itemRef={getItemRefCallback(index)}
                onSelect={() => {
                  setOpen(false);
                  triggerRef.current?.focus();
                }}
              />
            );
          })}
        </span>
      )}
    </span>
  );
}
