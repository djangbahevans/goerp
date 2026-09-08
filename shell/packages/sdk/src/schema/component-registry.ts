import type { ComponentType } from "react";

// biome-ignore lint/suspicious/noExplicitAny: a registered component's own prop shape is owner-defined; callers narrow it after resolve().
export type RegisteredComponent = ComponentType<any>;

// Stand-in for defineModule().views, which doesn't exist in this SDK yet —
// module registration itself (defineModule) is unbuilt. Bulk actions'
// `component` field (view-system.md's "Bulk actions") resolves through
// this until the real module-registration API lands; a module registers
// its component here by name instead of via defineModule().
export class ComponentRegistry {
  private readonly components = new Map<string, RegisteredComponent>();

  register(name: string, component: RegisteredComponent): void {
    this.components.set(name, component);
  }

  resolve(name: string): RegisteredComponent {
    const entry = this.components.get(name);
    if (!entry) {
      throw new Error(`ComponentRegistry: unknown component "${name}"`);
    }
    return entry;
  }

  has(name: string): boolean {
    return this.components.has(name);
  }
}
