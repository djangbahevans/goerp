import type { ModuleDefinition } from "./module-types.js";

type NavigationFn = NonNullable<ModuleDefinition["navigation"]>;

// defineModule().navigation, keyed by module name — one owner per key, so
// re-registering the same module (hot reload) replaces rather than throws.
// Registration only: nothing yet reads this to build the rendered nav tree
// (ViewRegistry's navigationTree is manifest-only).
export class ModuleNavigationRegistry {
  private readonly byModule = new Map<string, NavigationFn>();

  register(moduleName: string, navigation: NavigationFn): void {
    this.byModule.set(moduleName, navigation);
  }

  resolve(moduleName: string): NavigationFn | undefined {
    return this.byModule.get(moduleName);
  }
}

export const moduleNavigationRegistry = new ModuleNavigationRegistry();
