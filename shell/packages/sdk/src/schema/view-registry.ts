import * as v from "valibot";
import { buildModelRegistry } from "./model-registry.js";
import { optionalNullable as opt } from "./optional-nullable.js";
import { resolveViewDeclaration, type ViewDeclaration } from "./resolve-view-declaration.js";
import { CREATE_PATH_SUFFIX, resolveRecordViewPath } from "./resolve-view-path.js";
import { buildResourceMetadataRegistry, type ResourceMetadataEntry } from "./resource-metadata-registry.js";
import { buildResourceRegistry, type ResourceRegistryEntry } from "./resource-registry.js";
import { summarizeIssues } from "./summarize-issues.js";
import type { MetaSchema, ModelDef, NotificationTypeSchema } from "./types.js";

export interface ResolvedView {
  module: string;
  viewName: string;
  viewType: string;
  declaration: ViewDeclaration;
  permissions: string[];
  bundleUrl: string | null;
  recordId?: string;
}

export interface NavigationItem {
  key: string;
  label: string;
  path: string;
  search?: Record<string, string>;
  defaultFilters?: Record<string, unknown>;
  icon: string;
  permission?: string;
  condition?: string;
  badgeCountRoute?: string;
  external?: boolean;
}

export interface NavigationGroup {
  key: string;
  label: string;
  icon: string;
  module: string;
  permission?: string;
  condition?: string;
  children: NavigationItem[];
}

export interface ViewRegistry {
  resolveRoute(path: string): ResolvedView | null;
  // Every module route that opens a view, keyed by its path; a create form's path ends in "/new".
  routes: ReadonlyMap<string, ResolvedView>;
  navigationTree: NavigationGroup[];
  resources: Map<string, ResourceRegistryEntry>;
  resourceMetadata: Map<string, ResourceMetadataEntry>;
  recordViewPath(viewName: string, moduleName: string): string | null;
  models: Map<string, ModelDef>;
  viewPermissions(viewName: string): string[];
  getBundleUrl(moduleName: string): string | null;
  getBundleSHA256(moduleName: string): string | null;
  getModuleDisplayName(moduleName: string): string | null;
  notificationTypes: NotificationTypeGroup[];
}

export interface NotificationTypeGroup {
  module: string;
  displayName: string;
  types: NotificationTypeEntry[];
}

export interface NotificationTypeEntry {
  type: string;
  label: string;
  description: string | null;
  availableChannels: string[];
}

const DEFAULT_GROUP_ICON = "folder";
const DEFAULT_ITEM_ICON = "circle";

const NavItemDeclarationSchema = v.looseObject({
  label: v.string(),
  icon: opt(v.string()),
  view: opt(v.string()),
  route: v.string(),
  permission: opt(v.string()),
  condition: opt(v.string()),
  badge_count_route: opt(v.string()),
  external: opt(v.boolean()),
  default_filters: opt(v.record(v.string(), v.unknown())),
});

const NavGroupDeclarationSchema = v.looseObject({
  label: v.string(),
  icon: opt(v.string()),
  order: v.number(),
  permission: opt(v.string()),
  condition: opt(v.string()),
  children: v.array(NavItemDeclarationSchema),
});

function expandNavPath(moduleName: string, route: string): string {
  const modulePath = route === "/" ? `/${moduleName}` : `/${moduleName}${route}`;
  return `/_m${modulePath}`;
}

function splitNavRoute(route: string): { route: string; search?: Record<string, string> } {
  const split = route.indexOf("?");
  if (split < 0 || split === route.length - 1) return { route: split < 0 ? route : route.slice(0, split) };
  return { route: route.slice(0, split), search: Object.fromEntries(new URLSearchParams(route.slice(split + 1))) };
}

function slugify(label: string): string {
  return label
    .toLowerCase()
    .replaceAll(/[^a-z0-9]+/g, "-")
    .replaceAll(/(^-|-$)/g, "");
}

function buildNavTree(schema: MetaSchema): NavigationGroup[] {
  const groups: { order: number; group: NavigationGroup }[] = [];

  for (const [moduleName, moduleSchema] of Object.entries(schema.modules)) {
    for (const raw of moduleSchema.navigation) {
      const result = v.safeParse(NavGroupDeclarationSchema, raw);
      if (!result.success) {
        console.warn(
          `buildNavTree: a navigation group in module "${moduleName}" doesn't match the NavGroup schema (manifest-spec.md §12) — ${summarizeIssues(result.issues)}`,
        );
        continue;
      }
      const declaration = result.output;

      groups.push({
        order: declaration.order,
        group: {
          key: `${moduleName}:${slugify(declaration.label)}`,
          label: declaration.label,
          icon: declaration.icon ?? DEFAULT_GROUP_ICON,
          module: moduleName,
          ...(declaration.permission !== undefined ? { permission: declaration.permission } : {}),
          ...(declaration.condition !== undefined ? { condition: declaration.condition } : {}),
          children: declaration.children.map((item) => {
            const { route, search } = item.external
              ? { route: item.route, search: undefined }
              : splitNavRoute(item.route);
            return {
              key: `${moduleName}:${slugify(declaration.label)}:${slugify(item.label)}`,
              label: item.label,
              path: item.external ? item.route : expandNavPath(moduleName, route),
              ...(search !== undefined ? { search } : {}),
              ...(item.default_filters !== undefined ? { defaultFilters: item.default_filters } : {}),
              icon: item.icon ?? DEFAULT_ITEM_ICON,
              ...(item.permission !== undefined ? { permission: item.permission } : {}),
              ...(item.condition !== undefined ? { condition: item.condition } : {}),
              ...(item.badge_count_route !== undefined ? { badgeCountRoute: item.badge_count_route } : {}),
              ...(item.external !== undefined ? { external: item.external } : {}),
            };
          }),
        },
      });
    }
  }

  return groups.sort((a, b) => a.order - b.order).map(({ group }) => group);
}

const ACTION_ARRAY_KEYS = ["actions", "card_actions", "column_actions", "bulk_actions"] as const;

function isCreateAction(action: unknown): boolean {
  return typeof action === "object" && action !== null && (action as { type?: unknown }).type === "create";
}

export function filterViewByCapability(view: ViewDeclaration, resource: ResourceRegistryEntry | undefined): void {
  const mutable = view as ViewDeclaration & Record<string, unknown>;

  if (!resource?.listPath) {
    for (const key of ACTION_ARRAY_KEYS) {
      if (key in mutable) mutable[key] = [];
    }
    if ("columns" in mutable) mutable.columns = [];
    if ("quick_create" in mutable) mutable.quick_create = false;
    return;
  }

  if (!resource.createPath) {
    for (const key of ACTION_ARRAY_KEYS) {
      const actions = mutable[key];
      if (Array.isArray(actions)) mutable[key] = actions.filter((action) => !isCreateAction(action));
    }
    if ("quick_create" in mutable) mutable.quick_create = false;
  }
}

const TRAILING_ID_SEGMENT = "/{id}";

export { CREATE_PATH_SUFFIX } from "./resolve-view-path.js";

export function buildViewRegistry(schema: MetaSchema): ViewRegistry {
  const resources = buildResourceRegistry(schema);
  const models = buildModelRegistry(schema);
  const routeMap = new Map<string, ResolvedView>();
  // Concrete record URLs match templates after removing their final ID segment.
  const templatedRouteMap = new Map<string, ResolvedView>();
  const permissionsByView = new Map<string, string[]>();

  for (const [moduleName, moduleSchema] of Object.entries(schema.modules)) {
    for (const route of moduleSchema.routes) {
      // Write routes sharing a GET path must not replace its browser view.
      const opensCreateForm = route.crud_action === "create";
      if (!route.view || (route.method !== "GET" && !opensCreateForm)) continue;

      const declaration = resolveViewDeclaration(schema, route.view, moduleName);
      if (!declaration) continue;

      filterViewByCapability(declaration, resources.get(declaration.resource));

      const permissions = route.permissions;
      const resolved: ResolvedView = {
        module: moduleName,
        viewName: declaration.name,
        viewType: declaration.type,
        declaration,
        permissions,
        bundleUrl: moduleSchema.frontend?.bundle_url ?? null,
      };
      if (opensCreateForm) {
        routeMap.set(route.path + CREATE_PATH_SUFFIX, resolved);
        continue;
      }
      routeMap.set(route.path, resolved);
      if (route.path.endsWith(TRAILING_ID_SEGMENT)) {
        templatedRouteMap.set(route.path.slice(0, -TRAILING_ID_SEGMENT.length), resolved);
      }
      permissionsByView.set(`${moduleName}.${declaration.name}`, permissions);
    }
  }

  const navigationTree = buildNavTree(schema);

  return {
    resolveRoute: (path) => {
      const exact = routeMap.get(path);
      if (exact) return exact;

      const lastSlash = path.lastIndexOf("/");
      if (lastSlash === -1) return null;
      const id = path.slice(lastSlash + 1);
      if (!id) return null;

      const templated = templatedRouteMap.get(path.slice(0, lastSlash));
      return templated ? { ...templated, recordId: id } : null;
    },
    routes: routeMap,
    navigationTree,
    resources,
    resourceMetadata: buildResourceMetadataRegistry(schema),
    recordViewPath: (viewName, moduleName) => resolveRecordViewPath(schema, viewName, moduleName),
    models,
    viewPermissions: (viewName) => permissionsByView.get(viewName) ?? [],
    getBundleUrl: (moduleName) => schema.modules[moduleName]?.frontend?.bundle_url ?? null,
    getBundleSHA256: (moduleName) => schema.modules[moduleName]?.frontend?.bundle_sha256 ?? null,
    getModuleDisplayName: (moduleName) => schema.modules[moduleName]?.display_name ?? null,
    notificationTypes: buildNotificationTypeGroups(schema),
  };
}

const ENGINE_NOTIFICATION_MODULE = "engine";

function buildNotificationTypeGroups(schema: MetaSchema): NotificationTypeGroup[] {
  const toEntries = (moduleName: string, types: NotificationTypeSchema[]): NotificationTypeEntry[] =>
    types.map((nt) => ({
      type: `${moduleName}.${nt.name}`,
      label: nt.label,
      description: nt.description ?? null,
      availableChannels: nt.available_channels,
    }));

  const moduleGroups = Object.entries(schema.modules)
    .filter(([, m]) => m.notification_types.length > 0)
    .map(([moduleName, m]) => ({
      module: moduleName,
      displayName: m.display_name || moduleName,
      types: toEntries(moduleName, m.notification_types),
    }))
    .sort((a, b) => a.displayName.localeCompare(b.displayName));

  const engineTypes = schema.engine_notification_types ?? [];
  if (engineTypes.length === 0) return moduleGroups;
  return [
    {
      module: ENGINE_NOTIFICATION_MODULE,
      displayName: "General",
      types: toEntries(ENGINE_NOTIFICATION_MODULE, engineTypes),
    },
    ...moduleGroups,
  ];
}

export function buildEmptyViewRegistry(): ViewRegistry {
  return {
    resolveRoute: () => null,
    routes: new Map(),
    navigationTree: [],
    resources: new Map(),
    resourceMetadata: new Map(),
    recordViewPath: () => null,
    models: new Map(),
    viewPermissions: () => [],
    getBundleUrl: () => null,
    getBundleSHA256: () => null,
    getModuleDisplayName: () => null,
    notificationTypes: [],
  };
}
