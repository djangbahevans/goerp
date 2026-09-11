import { Check } from "lucide-react";
import type { KeyboardEvent, ReactNode, Ref } from "react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useOptionalPermission } from "../auth/use-permission.js";
import { actionButtonClassName } from "./action-button-styles.js";
import { Icon, type IconNameLike } from "./icon.js";

export interface ActionMenuItem {
  type?: "item" | "separator" | undefined;
  // Required for "item"; absent (and unused) for "separator".
  label?: string | undefined;
  // Lucide icon name, shown before the label.
  icon?: IconNameLike | undefined;
  onClick?: (() => void) | undefined;
  variant?: "default" | "danger" | undefined;
  permission?: string | undefined;
  disabled?: boolean | undefined;
  // chrome-header.md's UserMenu theme-toggle item: an extension of this
  // shape, not a new component. Present (boolean, not undefined) switches
  // the item to role="menuitemcheckbox" with a trailing checkmark.
  checked?: boolean | undefined;
}

// Renders the trigger button when a caller needs a different visual (e.g.
// UserMenu's avatar+chevron) than the default labeled button — every
// keyboard/open-state mechanic below is unchanged either way.
export interface ActionMenuTriggerProps {
  ref: Ref<HTMLButtonElement>;
  open: boolean;
  disabled: boolean;
  onClick: () => void;
  onKeyDown: (event: KeyboardEvent<HTMLButtonElement>) => void;
}

export interface ActionMenuProps {
  label: string;
  items: ActionMenuItem[];
  disabled?: boolean | undefined;
  trigger?: ((props: ActionMenuTriggerProps) => ReactNode) | undefined;
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

  const handleClick = () => {
    if (item.disabled) return;
    item.onClick?.();
    onSelect();
  };

  // Two literal branches, not one role={checkable ? ... : ...} — role and
  // aria-checked must move together (aria-checked is invalid on a plain
  // menuitem), which is clearer as two fixed shapes than one dynamically
  // correlated pair.
  if (item.checked !== undefined) {
    return (
      <button
        ref={itemRef}
        type="button"
        role="menuitemcheckbox"
        aria-checked={item.checked}
        tabIndex={tabIndex}
        data-variant={item.variant ?? "default"}
        aria-disabled={item.disabled}
        title={item.label}
        className={`${ITEM_CLASSES} justify-between`}
        onClick={handleClick}
      >
        <span className="flex items-center gap-2">
          {item.icon && <Icon name={item.icon} size={14} className="flex-none" aria-hidden="true" />}
          {item.label}
        </span>
        {item.checked && <Check size={14} aria-hidden="true" className="flex-none" />}
      </button>
    );
  }

  return (
    <button
      ref={itemRef}
      type="button"
      role="menuitem"
      tabIndex={tabIndex}
      data-variant={item.variant ?? "default"}
      // Not the native `disabled` attribute — that would remove the item
      // from focus entirely, contradicting the ARIA APG's disabled-menuitem
      // convention this component follows: reachable by keyboard, just
      // inert on activation.
      aria-disabled={item.disabled}
      title={item.label}
      className={ITEM_CLASSES}
      onClick={handleClick}
    >
      {item.icon && <Icon name={item.icon} size={14} className="flex-none" aria-hidden="true" />}
      {item.label}
    </button>
  );
}

export function ActionMenu({ label, items, disabled = false, trigger }: ActionMenuProps): ReactNode {
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const panelRef = useRef<HTMLSpanElement | null>(null);
  // Portaled to document.body, position: fixed — so a trigger nested in a
  // scrolling ancestor doesn't get its panel clipped. Null until measured,
  // so it renders hidden for one frame rather than flashing at (0, 0).
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
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

  // Runs before paint, positioned once on open — not re-tracked on
  // scroll/resize, since the menu closes on Escape/selection well before
  // either would matter.
  useLayoutEffect(() => {
    if (!open) {
      setPosition(null);
      return;
    }
    const triggerEl = triggerRef.current;
    const panelEl = panelRef.current;
    if (!triggerEl || !panelEl) return;
    const triggerRect = triggerEl.getBoundingClientRect();
    const panelRect = panelEl.getBoundingClientRect();
    const maxLeft = window.innerWidth - panelRect.width - 8;
    const left = Math.max(8, Math.min(triggerRect.left, maxLeft));
    // Opens upward instead when there isn't room below for the panel's own
    // measured height, same collision-avoidance idea as the left clamp.
    const fitsBelow = triggerRect.bottom + 4 + panelRect.height <= window.innerHeight - 8;
    const top = fitsBelow ? triggerRect.bottom + 4 : Math.max(8, triggerRect.top - 4 - panelRect.height);
    setPosition({ top, left });
  }, [open]);

  // Closes on a click outside both the trigger and the panel — mousedown,
  // not click, so it commits before any outside element's own click
  // handler fires. Never existed pre-portal either; not just a portal gap.
  useEffect(() => {
    if (!open) return;
    function handlePointerDown(event: MouseEvent): void {
      const target = event.target as Node;
      if (triggerRef.current?.contains(target) || panelRef.current?.contains(target)) return;
      setOpen(false);
    }
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
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
      {trigger ? (
        trigger({
          ref: triggerRef,
          open,
          disabled,
          // Guarded here, not left to each custom trigger to apply itself.
          onClick: () => {
            if (disabled) return;
            open ? setOpen(false) : openMenu(1);
          },
          onKeyDown: (event) => {
            if (disabled) return;
            handleTriggerKeyDown(event);
          },
        })
      ) : (
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
      )}
      {open &&
        createPortal(
          <span
            ref={panelRef}
            role="menu"
            onKeyDown={handleMenuKeyDown}
            style={
              position
                ? { position: "fixed", top: position.top, left: position.left }
                : { position: "fixed", top: 0, left: 0, visibility: "hidden" }
            }
            className="z-(--z-dropdown) min-w-40 max-w-70 rounded-structural border border-border bg-surface py-1 shadow-md"
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
          </span>,
          document.body,
        )}
    </span>
  );
}
