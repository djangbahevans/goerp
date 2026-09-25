// defineModule().api, keyed by module name. Re-registering the same module
// (hot reload) replaces its client and notifies subscribers, so a mounted
// useModule picks up the new one.
export class ModuleApiRegistry {
  private readonly byModule = new Map<string, object>();
  private readonly listeners = new Set<() => void>();

  // Accepts undefined so a hot-reloaded module that stops declaring api
  // clears its prior bundle's client.
  register(moduleName: string, api: object | undefined): void {
    if (this.byModule.get(moduleName) === api) return;
    if (api) this.byModule.set(moduleName, api);
    else this.byModule.delete(moduleName);
    for (const listener of this.listeners) listener();
  }

  resolve(moduleName: string): object | undefined {
    return this.byModule.get(moduleName);
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
}

export const moduleApiRegistry = new ModuleApiRegistry();
