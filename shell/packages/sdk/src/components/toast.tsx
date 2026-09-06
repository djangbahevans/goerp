import type { ReactNode } from "react";
import { useEffect, useState } from "react";
import type { ToastMessage } from "../notifications/toast.js";
import { type ToastBus, toastBus } from "../notifications/toast.js";

export interface ToastProps {
  // Defaults to the real singleton every `toast.success/error/...` call
  // (@goerp/sdk/notifications) targets. Overridable for tests, so they
  // don't share state with the global singleton or other test files.
  bus?: ToastBus | undefined;
}

// shell-ux.md §7.1: renders the ToastBus queue — the data/event layer
// module code calls directly (`toast.success(...)`), never mounting this
// itself. Meant to be mounted once, by the shell's own root layout.
export function Toast({ bus = toastBus }: ToastProps): ReactNode {
  const [toasts, setToasts] = useState<ToastMessage[]>(() => bus.getToasts());

  useEffect(() => {
    setToasts(bus.getToasts());
    return bus.subscribe(setToasts);
  }, [bus]);

  if (toasts.length === 0) return null;

  return (
    <section aria-label="Notifications">
      {toasts.map((t) => (
        <div key={t.id} role={t.variant === "error" ? "alert" : "status"} data-variant={t.variant}>
          <span>{t.message}</span>
          {t.options?.action && (
            <button type="button" onClick={t.options.action.onClick}>
              {t.options.action.label}
            </button>
          )}
          {t.variant !== "loading" && (
            // A loading toast is resolved by the caller (success/error
            // with the same id), not by the user — letting it be
            // manually dismissed would make that later resolve() call
            // resurrect it as a brand-new toast (id no longer found in
            // ToastBus.toasts, so upsert() appends instead of replacing).
            <button type="button" onClick={() => bus.dismiss(t.id)} aria-label={`Dismiss: ${t.message}`}>
              ×
            </button>
          )}
        </div>
      ))}
    </section>
  );
}
