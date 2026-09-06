import type { SchemaRegistry } from "./schema-registry.js";
import type { MetaSchema } from "./types.js";

// A manifest view declaration's JSON shape — callers narrow on `.type`.
export type ViewDeclaration = Record<string, unknown> & { type: string };

function isViewDeclaration(value: unknown): value is ViewDeclaration {
  return typeof value === "object" && value !== null && typeof (value as { type?: unknown }).type === "string";
}

// Resolves a view reference ({view_name} or {module}.{view_name}) to its
// full declaration, same splitting convention as resolveViewPath.
export function resolveViewDeclaration(
  schema: MetaSchema,
  viewRef: string,
  currentModule: string,
): ViewDeclaration | null {
  const dotIndex = viewRef.indexOf(".");
  const moduleName = dotIndex < 0 ? currentModule : viewRef.slice(0, dotIndex);
  const viewName = dotIndex < 0 ? viewRef : viewRef.slice(dotIndex + 1);

  const moduleSchema = schema.modules[moduleName];
  if (!moduleSchema) return null;

  const view = moduleSchema.views.find((v) => isViewDeclaration(v) && v.name === viewName);
  return isViewDeclaration(view) ? view : null;
}

export class ViewDeclarationRegistry {
  constructor(private readonly schema: Pick<SchemaRegistry, "getSchema">) {}

  async resolve(viewRef: string, currentModule: string): Promise<ViewDeclaration | null> {
    const schema = await this.schema.getSchema();
    return resolveViewDeclaration(schema, viewRef, currentModule);
  }
}
