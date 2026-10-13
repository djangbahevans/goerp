import type { ErrorHandler } from "./module-types.js";

export class ModuleErrorHandlerRegistry {
  private readonly byModule = new Map<string, Partial<Record<string, ErrorHandler>>>();

  register(moduleName: string, handlers: Partial<Record<string, ErrorHandler>> | undefined): void {
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
