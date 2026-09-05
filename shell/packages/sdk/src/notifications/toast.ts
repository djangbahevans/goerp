export interface ToastOptions {
  duration?: number;
}

export type ToastVariant = "success" | "error" | "warning" | "info";

export interface ToastMessage {
  id: string;
  variant: ToastVariant;
  message: string;
  options: ToastOptions | undefined;
}

// typescript-sdk-reference.md §13 "Feedback components" — the API a
// future Toast UI component renders from. No such component exists yet;
// this is the data/event layer alone.
export interface ToastAPI {
  success(message: string, options?: ToastOptions): void;
  error(message: string, options?: ToastOptions): void;
  warning(message: string, options?: ToastOptions): void;
  info(message: string, options?: ToastOptions): void;
  dismiss(id?: string): void;
}

type Listener = (toasts: ToastMessage[]) => void;

export class ToastBus implements ToastAPI {
  private toasts: ToastMessage[] = [];
  private readonly listeners = new Set<Listener>();

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

  dismiss(id?: string): void {
    this.toasts = id === undefined ? [] : this.toasts.filter((t) => t.id !== id);
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

  private push(variant: ToastVariant, message: string, options?: ToastOptions): void {
    this.toasts = [...this.toasts, { id: crypto.randomUUID(), variant, message, options }];
    this.notify();
  }

  private notify(): void {
    for (const listener of this.listeners) listener(this.toasts);
  }
}

export const toast: ToastAPI = new ToastBus();
