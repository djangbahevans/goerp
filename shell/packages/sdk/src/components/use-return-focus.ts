import { useState } from "react";

// onCloseAutoFocus for a Radix dialog with no Dialog.Trigger: focuses `returnFocusTo` (null: nothing), else
// the element focused when `open` turned true, else <main> if that left the page and focus is lost.
export function useReturnFocus(open: boolean, returnFocusTo?: HTMLElement | null): (event: Event) => void {
  // Captured during render: Radix skips onOpenAutoFocus when a child with
  // autoFocus already took focus, so no open-time event sees the opener.
  const [opener, setOpener] = useState<{ open: boolean; element: HTMLElement | null }>({
    open: false,
    element: null,
  });
  if (opener.open !== open) {
    setOpener({
      open,
      element: open && document.activeElement instanceof HTMLElement ? document.activeElement : opener.element,
    });
  }

  return (event) => {
    event.preventDefault();
    if (returnFocusTo === null) return;
    const target = returnFocusTo ?? opener.element;
    if (target?.isConnected) {
      target.focus();
      return;
    }
    // The page may already have moved focus on (to the next row after a delete).
    const active = document.activeElement;
    if (!active || active === document.body || !active.isConnected)
      document.querySelector<HTMLElement>("main")?.focus();
  };
}
