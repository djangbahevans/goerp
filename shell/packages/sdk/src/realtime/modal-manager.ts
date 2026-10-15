export interface FormModalConfig {
  resource: string;
  recordId?: string;
}

export type DialogFieldType = "text" | "textarea" | "number" | "monetary" | "phone" | "date" | "select" | "boolean";

export interface DialogField {
  name: string;
  label: string;
  type: DialogFieldType;
  required?: boolean;
  placeholder?: string;
  // May hold {record.field} and {today} placeholders.
  default?: string;
  help?: string;
  options?: { value: string; label: string }[];
}

export interface DialogAction {
  method: string;
  path: string;
}

export interface DialogConfig {
  title: string;
  submitLabel: string;
  action: DialogAction;
  fields: DialogField[];
  record: Record<string, unknown> | null;
  // Server-provided initial values; they take precedence over each field's default.
  values: Record<string, unknown>;
}

export interface DialogResult {
  // True when the user dismissed the dialog without submitting.
  cancelled: boolean;
}

type ModalContent =
  | { kind: "form"; config: FormModalConfig }
  | { kind: "dialog"; config: DialogConfig; resolve: (result: DialogResult) => void };

export type ModalEntry = ModalContent & { id: number };

// A stack of open modals, readable from non-React code (the UI instruction
// executor) and from React through useSyncExternalStore.
export class ModalManager {
  private stack: readonly ModalEntry[] = [];
  private nextId = 0;
  private readonly submitting = new Set<number>();
  private readonly listeners = new Set<() => void>();

  open(config: FormModalConfig): void {
    this.push({ kind: "form", config });
  }

  // Resolves once the dialog closes: on cancel, or after a submit and the
  // instructions in its response have finished.
  openDialog(config: DialogConfig): Promise<DialogResult> {
    return new Promise((resolve) => this.push({ kind: "dialog", config, resolve }));
  }

  closeTop(): void {
    const top = this.stack.at(-1);
    if (top) this.close(top.id);
  }

  // Closes the modal with this id. A dialog resolves as cancelled, unless a
  // submit is in flight: then the submit settles it.
  close(id: number): void {
    const entry = this.stack.find((e) => e.id === id);
    if (!entry) return;
    this.stack = this.stack.filter((e) => e !== entry);
    if (entry.kind === "dialog" && !this.submitting.has(id)) entry.resolve({ cancelled: true });
    this.notify();
  }

  // Runs a dialog's submit. The dialog resolves as submitted once `submit`
  // resolves, even if a close_modal in the response closed it meanwhile, and
  // closes then if it is still open. A rejected submit leaves the dialog open
  // and rethrows.
  async submitDialog(id: number, submit: () => Promise<unknown>): Promise<void> {
    const entry = this.stack.find((e) => e.id === id);
    if (entry?.kind !== "dialog") throw new Error(`modal ${id} is not an open dialog`);
    this.submitting.add(id);
    try {
      await submit();
    } catch (err) {
      this.submitting.delete(id);
      if (!this.stack.includes(entry)) entry.resolve({ cancelled: true });
      throw err;
    }
    this.submitting.delete(id);
    entry.resolve({ cancelled: false });
    this.close(id);
  }

  getStack = (): readonly ModalEntry[] => this.stack;

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  private push(entry: ModalContent): void {
    this.stack = [...this.stack, { ...entry, id: this.nextId++ }];
    this.notify();
  }

  private notify(): void {
    for (const listener of this.listeners) listener();
  }
}

export const modalManager = new ModalManager();
