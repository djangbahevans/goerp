import type { ErrorHandler } from "./module-types.js";

// defineModule().errorHandlers, keyed by module name then exact AppError.code
// (with "*" as that module's wildcard fallback) — use-action.ts's onError
// consults this as a fallback. One owner per module-name key, so re-registering
// (hot reload) replaces rather than throws.
export class ModuleErrorHandlerRegistry {
  private readonly byModule = new Map<string, Record<string, ErrorHandler>>();

  // Accepts undefined so a hot-reloaded module that stops declaring
  // errorHandlers clears its old entry, instead of a falsy value being
  // skipped and leaving the prior bundle's handlers active.
  register(moduleName: string, handlers: Record<string, ErrorHandler> | undefined): void {
    if (handlers) this.byModule.set(moduleName, handlers);
    else this.byModule.delete(moduleName);
  }

  resolve(moduleName: string, code: string): ErrorHandler | undefined {
    const handlers = this.byModule.get(moduleName);
    if (!handlers) return undefined;
    return handlers[code] ?? handlers["*"];
  }
}

export const moduleErrorHandlerRegistry = new ModuleErrorHandlerRegistry();
