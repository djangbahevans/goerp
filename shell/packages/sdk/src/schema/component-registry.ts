import type { ComponentType } from "react";

// biome-ignore lint/suspicious/noExplicitAny: a registered component's own prop shape is owner-defined; callers narrow it after resolve().
export type RegisteredComponent = ComponentType<any>;

// Backs defineModule().views/.fieldRenderers (../module/define-module.js
// registers directly into this shared singleton) and bulk actions'
// `component` field (view-system.md's "Bulk actions").
export class ComponentRegistry {
  private readonly components = new Map<string, RegisteredComponent>();

  // Throws on a name collision rather than silently letting the second
  // registration win — two modules independently registering the same
  // component name is a real cross-module conflict, not something this
  // provisional, unnamespaced registry should paper over.
  register(name: string, component: RegisteredComponent): void {
    if (this.components.has(name)) {
      throw new Error(`ComponentRegistry: "${name}" is already registered`);
    }
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

  // A declared-but-possibly-unregistered name (a manifest `component` field
  // left unset, or naming a module that hasn't loaded) is the common case at
  // most call sites — this collapses their has()-then-resolve() into one
  // lookup and returns undefined instead of resolve()'s throw.
  tryResolve(name: string | undefined): RegisteredComponent | undefined {
    return name ? this.components.get(name) : undefined;
  }

  // For a caller that tracks its own previously-registered names (e.g.
  // defineModule() re-registering a hot-reloaded module) to clear them
  // before registering again, rather than hitting the collision guard.
  unregister(name: string): void {
    this.components.delete(name);
  }
}
