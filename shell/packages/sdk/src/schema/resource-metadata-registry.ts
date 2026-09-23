import { cacheUntilRejected } from "./cached-promise.js";
import { buildModelRegistry } from "./model-registry.js";
import { buildResourceRegistry } from "./resource-registry.js";
import type { SchemaRegistry } from "./schema-registry.js";
import type { FieldDef, MetaSchema } from "./types.js";

interface ResourceViewDeclaration {
  name: string;
  type: string;
  resource: string;
  label_field?: string;
  search_param?: string;
  columns?: { field: string; primary?: boolean }[];
}

function isResourceView(value: unknown): value is ResourceViewDeclaration {
  if (typeof value !== "object" || value === null) return false;
  const v = value as Record<string, unknown>;
  return typeof v.name === "string" && typeof v.type === "string" && typeof v.resource === "string";
}

// manifest-spec.md §8b names this type `ResourceRegistryEntry`, but that
// name is already taken by goerp#638's CRUD-route-only registry
// (resource-registry.ts), which this one extends rather than replaces.
export interface ResourceMetadataEntry {
  module: string;
  resource: string;
  listRoute: string;
  getRoute: string;
  defaultListView: string;
  defaultFormView: string;
  labelField: string;
  searchParam: string;
  fields: FieldDef[];
}

// Every registered entry has a non-empty listRoute — registration itself
// requires one (buildResourceMetadataRegistry) — so this only strips
// whatever method token is actually present, not a hardcoded "GET ": a
// hand-registered list route isn't guaranteed to use that method.
export function resourceListPath(entry: ResourceMetadataEntry): string | undefined {
  return entry.listRoute ? entry.listRoute.replace(/^\S+ /, "") : undefined;
}

const LABEL_FIELD_FALLBACKS = ["display_name", "name", "title"];
const DEFAULT_SEARCH_PARAM = "q";

function collectDefaultViews(
  schema: MetaSchema,
): Map<string, { list?: ResourceViewDeclaration; form?: ResourceViewDeclaration }> {
  const byResource = new Map<string, { list?: ResourceViewDeclaration; form?: ResourceViewDeclaration }>();
  for (const moduleSchema of Object.values(schema.modules)) {
    for (const view of moduleSchema.views) {
      if (!isResourceView(view)) continue;
      if (view.type !== "list" && view.type !== "form") continue;

      const entry = byResource.get(view.resource) ?? {};
      if (view.type === "list" && !entry.list) entry.list = view;
      if (view.type === "form" && !entry.form) entry.form = view;
      byResource.set(view.resource, entry);
    }
  }
  return byResource;
}

// manifest-spec.md §8b's labelField precedence chain, step 3: the model's
// own .Primary() field, between the list view's primary:true column and
// the display_name/name/title convention fallback — what resolves a label
// for a resource with no list view at all (view-system.md, "Label field
// when there's no list view at all").
function resolvePrimaryField(fields: FieldDef[]): string | undefined {
  return fields.find((f) => f.is_primary)?.name;
}

function resolveLabelField(listView: ResourceViewDeclaration | undefined, fields: FieldDef[]): string {
  if (listView?.label_field) return listView.label_field;

  const primaryColumn = listView?.columns?.find((c) => c.primary);
  if (primaryColumn) return primaryColumn.field;

  const primaryField = resolvePrimaryField(fields);
  if (primaryField) return primaryField;

  const fieldNames = new Set(fields.map((f) => f.name));
  for (const candidate of LABEL_FIELD_FALLBACKS) {
    if (fieldNames.has(candidate)) return candidate;
  }

  return "id";
}

export function buildResourceMetadataRegistry(schema: MetaSchema): Map<string, ResourceMetadataEntry> {
  const registry = new Map<string, ResourceMetadataEntry>();
  const defaultViews = collectDefaultViews(schema);
  const crudRoutes = buildResourceRegistry(schema);
  const models = buildModelRegistry(schema);

  for (const [moduleName, moduleSchema] of Object.entries(schema.modules)) {
    for (const resource of Object.keys(moduleSchema.models)) {
      const crudEntry = crudRoutes.get(resource);
      if (!crudEntry?.listPath) continue;

      const model = models.get(resource);
      if (!model) continue;

      const views = defaultViews.get(resource);

      registry.set(resource, {
        module: moduleName,
        resource,
        listRoute: `${crudEntry.listMethod} ${crudEntry.listPath}`,
        getRoute: crudEntry.getPath ? `GET ${crudEntry.getPath}` : "",
        defaultListView: views?.list?.name ?? "",
        defaultFormView: views?.form?.name ?? "",
        labelField: resolveLabelField(views?.list, model.fields),
        searchParam: views?.list?.search_param ?? DEFAULT_SEARCH_PARAM,
        fields: model.fields,
      });
    }
  }

  return registry;
}

export class ResourceMetadataRegistry {
  private readonly getRegistry: () => Promise<Map<string, ResourceMetadataEntry>>;

  constructor(schema: Pick<SchemaRegistry, "getSchema">) {
    this.getRegistry = cacheUntilRejected(() => schema.getSchema().then((s) => buildResourceMetadataRegistry(s)));
  }

  async resolve(resource: string): Promise<ResourceMetadataEntry | undefined> {
    const registry = await this.getRegistry();
    return registry.get(resource);
  }
}
