import { AppError } from "../error/app-error.js";
import type { ModuleApis } from "../names.js";

export function createMockApi<M extends keyof ModuleApis>(
  module: M,
  overrides?: Partial<ModuleApis[M]>,
): ModuleApis[M] {
  const target = { ...overrides };
  const missing = new Map<string, () => Promise<never>>();

  return new Proxy(target, {
    get(object, property, receiver) {
      if (Object.hasOwn(object, property)) return Reflect.get(object, property, receiver);
      // A mock client must not become a thenable when a test awaits or returns it.
      if (typeof property !== "string" || property === "then") return undefined;
      let reject = missing.get(property);
      if (!reject) {
        reject = async () => {
          throw new AppError({
            code: "testing.unmocked_request",
            message: `Unmocked API function: ${String(module)}.${property}`,
            httpStatus: 501,
          });
        };
        missing.set(property, reject);
      }
      return reject;
    },
  }) as ModuleApis[M];
}
