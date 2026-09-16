import type { Command } from "./command-types.js";

// Backs defineModule().commands — @goerp/sdk can't import this directly
// (CommandRegistry is shell-app-owned, the real rendered Cmd+K palette),
// so bootstrap/register-module.js is the one place that maps a loaded
// module's ModuleDefinition.commands into this registry; see that file's
// own comment for why.
export class CommandRegistry {
  private readonly batches = new Set<Command[]>();
  private readonly listeners = new Set<() => void>();
  // Cached so getAll() returns a stable reference between mutations —
  // useSyncExternalStore's snapshot getter must not allocate a fresh
  // array on every call, or every render looks like a store change.
  private snapshot: Command[] = [];

  // Returns an unregister function, so a batch (and a hot-reloaded
  // module's own re-registration) can be cleanly replaced rather than
  // accumulating stale entries.
  register(commands: Command[]): () => void {
    this.batches.add(commands);
    this.notify();
    return () => {
      this.batches.delete(commands);
      this.notify();
    };
  }

  getAll(): Command[] {
    return this.snapshot;
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private notify(): void {
    this.snapshot = [...this.batches].flat();
    for (const listener of this.listeners) listener();
  }
}

export const commandRegistry = new CommandRegistry();
