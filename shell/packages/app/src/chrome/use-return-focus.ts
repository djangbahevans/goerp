import { useRef } from "react";

interface ReturnFocusHandlers {
  onOpenAutoFocus: () => void;
  onCloseAutoFocus: (event: Event) => void;
}

// Radix restores focus only to a Dialog.Trigger, which a dialog opened from
// code has none of, so the opener is captured before focus moves in. An opener
// that has since left the document falls back to the page's <main>.
export function useReturnFocus(): ReturnFocusHandlers {
  const opener = useRef<HTMLElement | null>(null);
  return {
    onOpenAutoFocus: () => {
      opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    },
    onCloseAutoFocus: (event) => {
      event.preventDefault();
      const target = opener.current?.isConnected ? opener.current : document.querySelector<HTMLElement>("main");
      target?.focus();
    },
  };
}
