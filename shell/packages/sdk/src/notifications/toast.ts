export interface ToastAction {
  label: string;
  onClick: () => void;
}

export interface ToastOptions {
  duration?: number;
  // Targets an existing toast — replaces it in place (same position,
  // variant/message updated) instead of appending a new one. The loading
  // → resolved pattern (shell-ux.md §7.1): `loading()` returns an id,
  // passed back here to resolve it.
  id?: string;
  action?: ToastAction;
}

export type ToastVariant = "success" | "error" | "warning" | "info" | "loading";

export interface ToastMessage {
  id: string;
  variant: ToastVariant;
  message: string;
  options: ToastOptions | undefined;
}

// typescript-sdk-reference.md §13 "Feedback components" / shell-ux.md
// §7.1 — the API the `Toast` component (../components/toast.tsx) renders
// from.
export interface ToastAPI {
  success(message: string, options?: ToastOptions): void;
  error(message: string, options?: ToastOptions): void;
  warning(message: string, options?: ToastOptions): void;
  info(message: string, options?: ToastOptions): void;
  // Does not auto-dismiss — returns an id to resolve later via success/error
  // with { id }.
  loading(message: string): string;
  dismiss(id?: string): void;
}

// shell-ux.md §7.1: default auto-dismiss durations per variant; loading
// never auto-dismisses on its own.
const DEFAULT_DURATIONS: Record<Exclude<ToastVariant, "loading">, number> = {
  success: 4000,
  warning: 6000,
  error: 8000,
  info: 4000,
};

// shell-ux.md §7.1: "Maximum 5 toasts visible at once; older ones ...
// are pushed off the stack."
const MAX_VISIBLE = 5;

type Listener = (toasts: ToastMessage[]) => void;

export class ToastBus implements ToastAPI {
  private toasts: ToastMessage[] = [];
  private readonly listeners = new Set<Listener>();
  private readonly timers = new Map<string, ReturnType<typeof setTimeout>>();

  success(message: string, options?: ToastOptions): void {
    this.push("success", message, options);
  }

  error(message: string, options?: ToastOptions): void {
    this.push("error", message, options);
  }

  warning(message: string, options?: ToastOptions): void {
    this.push("warning", message, options);
  }

  info(message: string, options?: ToastOptions): void {
    this.push("info", message, options);
  }

  loading(message: string): string {
    const id = crypto.randomUUID();
    this.upsert(id, "loading", message, undefined);
    return id;
  }

  dismiss(id?: string): void {
    if (id === undefined) {
      for (const t of this.toasts) this.clearTimer(t.id);
      this.toasts = [];
    } else {
      this.clearTimer(id);
      this.toasts = this.toasts.filter((t) => t.id !== id);
    }
    this.notify();
  }

  getToasts(): ToastMessage[] {
    return this.toasts;
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  private push(variant: Exclude<ToastVariant, "loading">, message: string, options?: ToastOptions): void {
    this.upsert(options?.id ?? crypto.randomUUID(), variant, message, options);
  }

  // Inserts a new toast, or — when `id` matches an existing one (the
  // loading → resolved pattern) — replaces it in place at the same
  // position rather than re-appending at the end.
  private upsert(id: string, variant: ToastVariant, message: string, options: ToastOptions | undefined): void {
    this.clearTimer(id);
    const next: ToastMessage = { id, variant, message, options };
    const index = this.toasts.findIndex((t) => t.id === id);
    let toasts = index === -1 ? [...this.toasts, next] : this.toasts.map((t, i) => (i === index ? next : t));

    if (index === -1 && toasts.length > MAX_VISIBLE) {
      const dropped = toasts[0];
      if (dropped) this.clearTimer(dropped.id);
      toasts = toasts.slice(1);
    }

    this.toasts = toasts;
    this.scheduleAutoDismiss(next);
    this.notify();
  }

  private scheduleAutoDismiss(entry: ToastMessage): void {
    if (entry.variant === "loading") return;
    const duration = entry.options?.duration ?? DEFAULT_DURATIONS[entry.variant];
    this.timers.set(
      entry.id,
      setTimeout(() => this.dismiss(entry.id), duration),
    );
  }

  private clearTimer(id: string): void {
    const timer = this.timers.get(id);
    if (timer !== undefined) {
      clearTimeout(timer);
      this.timers.delete(id);
    }
  }

  private notify(): void {
    for (const listener of this.listeners) listener(this.toasts);
  }
}

const toastBus = new ToastBus();

// Narrowed to ToastAPI for calling code (module authors triggering
// toasts) — the `Toast` UI component needs the full ToastBus
// (subscribe/getToasts) and imports `toastBus` directly instead.
export const toast: ToastAPI = toastBus;
export { toastBus };
