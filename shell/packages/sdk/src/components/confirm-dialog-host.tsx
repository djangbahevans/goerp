import { type ReactNode, useEffect, useState, useSyncExternalStore } from "react";
import { type ConfirmQueue, type ConfirmRequest, confirmQueue } from "../react/use-confirm.js";
import { AlertDialog } from "./alert-dialog.js";

export interface ConfirmDialogHostProps {
  // Injectable for tests and stories.
  queue?: ConfirmQueue | undefined;
}

// Renders useConfirm()'s dialogs; mounted once by the shell's root layout.
// An answered request stays shown, closed, until its dialog has finished
// closing; only then does the next one open, as a fresh dialog. Swapping the
// content into a still-open dialog would leave focus on the confirm button
// just pressed, so a repeated Enter could answer a request the user hasn't read.
export function ConfirmDialogHost({ queue = confirmQueue }: ConfirmDialogHostProps): ReactNode {
  const current = useSyncExternalStore(queue.subscribe, queue.current, () => null);
  const [shown, setShown] = useState<ConfirmRequest | null>(null);

  useEffect(() => {
    if (current && !shown) setShown(current);
  }, [current, shown]);

  if (!shown) return null;
  const { id, options, returnFocus } = shown;
  const variant = options.variant ?? "default";
  return (
    <AlertDialog
      open={shown === current}
      title={options.title}
      description={options.description}
      confirmLabel={options.confirmLabel}
      cancelLabel={options.cancelLabel}
      confirmVariant={variant === "danger" ? "danger" : "primary"}
      tone={variant}
      requireTyping={options.requireTyping}
      returnFocusTo={returnFocus ?? undefined}
      onConfirm={() => queue.settle(id, true)}
      onCancel={() => queue.settle(id, false)}
      onClosed={() => setShown(null)}
    />
  );
}
