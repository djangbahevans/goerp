export type ConfirmVariant = "default" | "warning" | "danger";

// shell-ux.md §7.2.
export interface ConfirmOptions {
  title: string;
  description: string;
  confirmLabel?: string | undefined;
  cancelLabel?: string | undefined;
  variant?: ConfirmVariant | undefined;
  // A phrase the user must type exactly before confirming.
  requireTyping?: string | undefined;
}

export interface ConfirmRequest {
  id: number;
  options: ConfirmOptions;
  // The element focus returns to once this request's dialog closes.
  returnFocus: HTMLElement | null;
}

type Listener = () => void;

// One dialog at a time: a confirm() made while another is showing queues
// behind it, and ConfirmDialogHost shows each in turn.
export class ConfirmQueue {
  private requests: (ConfirmRequest & { resolve: (confirmed: boolean) => void })[] = [];
  private nextId = 1;
  private readonly listeners = new Set<Listener>();

  confirm = (options: ConfirmOptions): Promise<boolean> =>
    new Promise((resolve) => {
      this.requests = [...this.requests, { id: this.nextId++, options, returnFocus: this.focusTarget(), resolve }];
      this.notify();
    });

  current = (): ConfirmRequest | null => this.requests[0] ?? null;

  // Settles only the request that is showing, so a late click on a dialog
  // that already closed can't answer the one queued behind it.
  settle(id: number, confirmed: boolean): void {
    const [head, ...rest] = this.requests;
    if (head?.id !== id) return;
    this.requests = rest;
    head.resolve(confirmed);
    this.notify();
  }

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  // A call made from inside an open dialog inherits the queued requests'
  // target, so focus ends up back where the first dialog was opened from.
  private focusTarget(): HTMLElement | null {
    const active = typeof document === "undefined" ? null : document.activeElement;
    if (active instanceof HTMLElement && active !== document.body && !active.closest('[role="alertdialog"]')) {
      return active;
    }
    return this.requests.at(-1)?.returnFocus ?? null;
  }

  private notify(): void {
    for (const listener of this.listeners) listener();
  }
}

export const confirmQueue = new ConfirmQueue();

export interface UseConfirmResult {
  confirm: (options: ConfirmOptions) => Promise<boolean>;
}

const result: UseConfirmResult = { confirm: confirmQueue.confirm };

// Resolves true on confirm, and false on cancel, Escape or any other close.
// Needs ConfirmDialogHost mounted once, which the shell's root layout does.
export function useConfirm(): UseConfirmResult {
  return result;
}
