import * as v from "valibot";
import type { SchemaRegistry } from "./schema-registry.js";
import { summarizeIssues } from "./summarize-issues.js";
import type { MetaSchema } from "./types.js";

// manifest-spec.md §9's "Common view fields (all view types)" — the only
// part of a view declaration every view type shares. `looseObject` (not
// `object`) keeps every type-specific field (group_by, card_fields, ...)
// intact on the value, since each renderer's own manifest-types.ts casts
// and validates the rest — that's each renderer ticket's own concern, same
// scoping as this module's MetaSchema leaving `views` as `unknown[]`.
const CommonViewFieldsSchema = v.looseObject({
  name: v.string(),
  type: v.string(),
  resource: v.string(),
  label: v.string(),
  icon: v.optional(v.string()),
  permission: v.optional(v.string()),
});

// A manifest view declaration's JSON shape — callers narrow further on `.type`.
export type ViewDeclaration = v.InferOutput<typeof CommonViewFieldsSchema>;

function hasMatchingName(value: unknown, name: string): boolean {
  return typeof value === "object" && value !== null && (value as { name?: unknown }).name === name;
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

  // Matched by name first, independent of validity — so a view that
  // exists but fails the common-fields check is reported, not silently
  // treated the same as one that was never there at all (a caller like
  // resource-metadata-registry.ts's collectNavigatedResources would
  // otherwise drop that view's entire resource from the registry with no
  // error anywhere, the exact silent-failure mode this validation exists
  // to prevent).
  const candidate = moduleSchema.views.find((view) => hasMatchingName(view, viewName));
  if (candidate === undefined) return null;

  const result = v.safeParse(CommonViewFieldsSchema, candidate);
  if (!result.success) {
    console.warn(
      `resolveViewDeclaration: "${viewName}" in module "${moduleName}" doesn't match the common view fields (manifest-spec.md §9) — ${summarizeIssues(result.issues)}`,
    );
    return null;
  }
  return result.output;
}

export class ViewDeclarationRegistry {
  constructor(private readonly schema: Pick<SchemaRegistry, "getSchema">) {}

  async resolve(viewRef: string, currentModule: string): Promise<ViewDeclaration | null> {
    const schema = await this.schema.getSchema();
    return resolveViewDeclaration(schema, viewRef, currentModule);
  }
}
