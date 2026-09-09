import type { Command } from "./command-types.js";

// Stand-in for defineModule().commands[], which doesn't exist yet — module
// registration itself (defineModule) is unbuilt anywhere in the SDK. A
// module registers its commands here instead, until the real
// module-registration API lands (same posture as @goerp/sdk/schema's
// ComponentRegistry standing in for defineModule().views).
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
