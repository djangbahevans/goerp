import type { ErrorHandler } from "./module-types.js";

// defineModule().errorHandlers, keyed by module name then exact AppError.code
// (with "*" as that module's wildcard fallback) — use-action.ts's onError
// consults this as a fallback. One owner per module-name key, so re-registering
// (hot reload) replaces rather than throws.
export class ModuleErrorHandlerRegistry {
  private readonly byModule = new Map<string, Record<string, ErrorHandler>>();

  register(moduleName: string, handlers: Record<string, ErrorHandler>): void {
    this.byModule.set(moduleName, handlers);
  }

  resolve(moduleName: string, code: string): ErrorHandler | undefined {
    const handlers = this.byModule.get(moduleName);
    if (!handlers) return undefined;
    return handlers[code] ?? handlers["*"];
  }
}

export const moduleErrorHandlerRegistry = new ModuleErrorHandlerRegistry();
