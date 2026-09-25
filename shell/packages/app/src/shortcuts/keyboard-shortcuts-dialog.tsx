import { IconButton, MODAL_CONTENT_CLASSES, MODAL_OVERLAY_CLASSES } from "@goerp/sdk/components";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { onKeyboardShortcutsOpenRequest } from "./keyboard-shortcuts-control.js";
import { KeyboardShortcutsList } from "./keyboard-shortcuts-list.js";

// keyboard-shortcuts-dialog.md: a shell singleton opened through
// openKeyboardShortcuts().
export function KeyboardShortcutsDialog(): ReactNode {
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLElement | null>(null);

  useEffect(() => onKeyboardShortcutsOpenRequest(() => setOpen(true)), []);

  return (
    <DialogPrimitive.Root open={open} onOpenChange={setOpen}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className={MODAL_OVERLAY_CLASSES} />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          className={MODAL_CONTENT_CLASSES}
          // Captured on mount rather than on the open request: a user menu
          // item opens the dialog, then the menu unmounts it and refocuses
          // its trigger, which is where focus belongs on close.
          onOpenAutoFocus={() => {
            triggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            triggerRef.current?.focus();
          }}
        >
          <div className="flex max-h-[70vh] w-full max-w-120 flex-col rounded-structural border border-border bg-surface shadow-lg">
            <div className="flex items-center justify-between gap-4 border-border border-b px-6 py-4">
              <DialogPrimitive.Title className="font-semibold text-lg text-text">
                Keyboard shortcuts
              </DialogPrimitive.Title>
              <IconButton icon="x" label="Close" size="sm" onClick={() => setOpen(false)} />
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
              <KeyboardShortcutsList />
            </div>
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
