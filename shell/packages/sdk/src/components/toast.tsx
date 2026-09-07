import type { ReactNode } from "react";
import { useEffect, useRef, useState } from "react";
import type { ToastMessage, ToastVariant } from "../notifications/toast.js";
import { type ToastBus, toastBus } from "../notifications/toast.js";
import { actionButtonClassName } from "./action-button-styles.js";
import { Spinner } from "./spinner.js";

// Matches --duration-fast — ToastBus removes a dismissed/auto-dismissed
// toast from its own list immediately (notifications/toast.ts has no
// grace-period concept), so this component keeps a just-removed toast
// rendered, marked `data-leaving`, for one exit-animation's worth of time
// before dropping it locally too. Not read from the CSS token itself since
// no JS-facing export of the design-token values exists yet.
const EXIT_DURATION_MS = 150;

export interface ToastProps {
  // Defaults to the real singleton every `toast.success/error/...` call
  // (@goerp/sdk/notifications) targets. Overridable for tests, so they
  // don't share state with the global singleton or other test files.
  bus?: ToastBus | undefined;
}

// docs/components/toast.md "Tokens Used" — left-border color per variant.
// `loading` uses --color-primary via a real spinner instead of a static
// icon; the other variants have no rendered icon (no icon library is wired
// in yet, same posture as ActionButton/Badge's icon prop) so only the
// border carries their color.
const VARIANT_BORDER_CLASSES: Record<ToastVariant, string> = {
  success: "border-l-success",
  error: "border-l-danger",
  warning: "border-l-warning",
  info: "border-l-info",
  loading: "border-l-primary",
};

const DISMISS_CLASSES =
  "rounded-control p-1 text-text-secondary transition-colors duration-(--duration-fast) ease-out hover:text-text focus-visible:outline-none focus-visible:shadow-focus motion-reduce:transition-none";

// shell-ux.md §7.1: renders the ToastBus queue — the data/event layer
// module code calls directly (`toast.success(...)`), never mounting this
// itself. Meant to be mounted once, by the shell's own root layout.
export function Toast({ bus = toastBus }: ToastProps): ReactNode {
  const [toasts, setToasts] = useState<ToastMessage[]>(() => bus.getToasts());
  const [leaving, setLeaving] = useState<ToastMessage[]>([]);
  const timersRef = useRef(new Map<string, ReturnType<typeof setTimeout>>());

  useEffect(() => {
    const timers = timersRef.current;

    const sync = (next: ToastMessage[]) => {
      setToasts((prev) => {
        const nextIds = new Set(next.map((t) => t.id));
        const removed = prev.filter((t) => !nextIds.has(t.id));
        if (removed.length > 0) {
          setLeaving((cur) => [...cur, ...removed]);
          for (const t of removed) {
            timers.set(
              t.id,
              setTimeout(() => {
                setLeaving((cur) => cur.filter((c) => c.id !== t.id));
                timers.delete(t.id);
              }, EXIT_DURATION_MS),
            );
          }
        }
        return next;
      });
    };

    sync(bus.getToasts());
    const unsubscribe = bus.subscribe(sync);
    return () => {
      unsubscribe();
      for (const timer of timers.values()) clearTimeout(timer);
      timers.clear();
    };
  }, [bus]);

  const visible = [...toasts.map((t) => ({ t, leaving: false })), ...leaving.map((t) => ({ t, leaving: true }))];
  if (visible.length === 0) return null;

  return (
    <section
      aria-label="Notifications"
      className="fixed inset-x-0 bottom-0 z-(--z-toast) flex flex-col items-center gap-2 p-4 sm:inset-x-auto sm:right-4 sm:items-end"
    >
      {visible.map(({ t, leaving: isLeaving }) => (
        <div
          key={t.id}
          role={t.variant === "error" ? "alert" : "status"}
          data-variant={t.variant}
          className={`flex w-full max-w-sm items-start gap-3 rounded-control border-l-4 bg-surface px-4 py-3 shadow-md ${
            isLeaving
              ? "animate-[toast-slide-out_var(--duration-fast)_ease-in] motion-reduce:animate-[toast-fade-out_var(--duration-fast)_ease-in]"
              : "animate-[toast-slide-in_var(--duration-base)_ease-out] motion-reduce:animate-[toast-fade-in_var(--duration-base)_ease-out]"
          } ${VARIANT_BORDER_CLASSES[t.variant]}`}
        >
          {t.variant === "loading" && (
            <span className="text-primary">
              <Spinner size={16} />
            </span>
          )}
          <span className="flex-1 text-text">{t.message}</span>
          {t.options?.action && (
            <button type="button" className={actionButtonClassName("ghost", "sm")} onClick={t.options.action.onClick}>
              {t.options.action.label}
            </button>
          )}
          {t.variant !== "loading" && (
            // A loading toast is resolved by the caller (success/error
            // with the same id), not by the user — letting it be
            // manually dismissed would make that later resolve() call
            // resurrect it as a brand-new toast (id no longer found in
            // ToastBus.toasts, so upsert() appends instead of replacing).
            <button
              type="button"
              className={DISMISS_CLASSES}
              onClick={() => bus.dismiss(t.id)}
              aria-label={`Dismiss: ${t.message}`}
            >
              ×
            </button>
          )}
        </div>
      ))}
    </section>
  );
}
