import { cacheUntilRejected } from "./cached-promise.js";
import type { SchemaRegistry } from "./schema-registry.js";
import type { MetaSchema, ViewExtensionDef, ViewExtensionRef } from "./types.js";

// view-system.md §10 — a {extends, extension} reference joined to its own
// module's matching definition, plus the extending module's load_order so
// consumers can apply extensions dependencies-first without re-deriving
// the dependency graph. `definition` is undefined when `extension` names
// nothing in the declaring module's own view_extension_definitions — the
// declaring manifest is malformed, and callers skip the entry rather than
// throwing (view-system.md §10: "nothing renders, nothing throws").
export interface ViewExtensionEntry {
  module: string;
  loadOrder: number;
  ref: ViewExtensionRef;
  definition: ViewExtensionDef | undefined;
}

// buildViewExtensionRegistry indexes every module's view_extensions by
// target `{module}.{view_name}` (ref.extends), joined against that same
// module's own view_extension_definitions (manifest-spec.md §11: `extension`
// resolves within the declaring manifest, not globally) — mirrors
// buildResourceRegistry/buildModelRegistry's "one pure function over the
// whole MetaSchema" shape.
export function buildViewExtensionRegistry(schema: MetaSchema): Map<string, ViewExtensionEntry[]> {
  const byTarget = new Map<string, ViewExtensionEntry[]>();

  for (const [moduleName, moduleSchema] of Object.entries(schema.modules)) {
    const defsByName = new Map(moduleSchema.view_extension_definitions.map((def) => [def.name, def]));

    for (const ref of moduleSchema.view_extensions) {
      const entry: ViewExtensionEntry = {
        module: moduleName,
        loadOrder: moduleSchema.load_order,
        ref,
        definition: defsByName.get(ref.extension),
      };
      const existing = byTarget.get(ref.extends);
      if (existing) existing.push(entry);
      else byTarget.set(ref.extends, [entry]);
    }
  }

  // Dependencies-first, per module load_order — the shell applies
  // extensions in this order rather than declaration order.
  for (const entries of byTarget.values()) {
    entries.sort((a, b) => a.loadOrder - b.loadOrder);
  }

  return byTarget;
}

// Fetches and caches the view-extension registry for the process lifetime,
// sharing schemaRegistry's own cached schema fetch — same convention as
// ResourceRegistry/ViewDeclarationRegistry.
export class ViewExtensionRegistry {
  private readonly getRegistry: () => Promise<Map<string, ViewExtensionEntry[]>>;

  constructor(schema: Pick<SchemaRegistry, "getSchema">) {
    this.getRegistry = cacheUntilRejected(() => schema.getSchema().then((s) => buildViewExtensionRegistry(s)));
  }

  // Entries targeting `{module}.{viewName}`, in application order
  // (dependencies first) — [] when nothing extends this view.
  async forTarget(module: string, viewName: string): Promise<ViewExtensionEntry[]> {
    const registry = await this.getRegistry();
    return registry.get(`${module}.${viewName}`) ?? [];
  }
}
