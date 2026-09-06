import { cacheUntilRejected } from "./cached-promise.js";
import type { SchemaRegistry } from "./schema-registry.js";
import type { MetaSchema, ModelDef } from "./types.js";

// Each module's `models` map is already keyed by the fully-qualified
// "module.model" name, so building the flat registry is a plain merge.
export function buildModelRegistry(schema: MetaSchema): Map<string, ModelDef> {
  const registry = new Map<string, ModelDef>();
  for (const moduleSchema of Object.values(schema.modules)) {
    for (const [resource, model] of Object.entries(moduleSchema.models)) {
      registry.set(resource, model);
    }
  }
  return registry;
}

// Caches the model registry for the process lifetime, like ResourceRegistry.
export class ModelRegistry {
  private readonly getRegistry: () => Promise<Map<string, ModelDef>>;

  constructor(schema: Pick<SchemaRegistry, "getSchema">) {
    this.getRegistry = cacheUntilRejected(() => schema.getSchema().then((s) => buildModelRegistry(s)));
  }

  async resolve(resource: string): Promise<ModelDef> {
    const registry = await this.getRegistry();
    const entry = registry.get(resource);
    if (!entry) {
      throw new Error(`ModelRegistry: unknown resource "${resource}"`);
    }
    return entry;
  }
}
