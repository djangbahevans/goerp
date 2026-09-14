import * as v from "valibot";
import { buildModelRegistry } from "./model-registry.js";
import { optionalNullable as opt } from "./optional-nullable.js";
import { resolveViewDeclaration, type ViewDeclaration } from "./resolve-view-declaration.js";
import { buildResourceRegistry, type ResourceRegistryEntry } from "./resource-registry.js";
import { summarizeIssues } from "./summarize-issues.js";
import type { MetaSchema, ModelDef } from "./types.js";

// shell-architecture.md §9's ResolvedView — a browser path resolved all
// the way through to the view declaration that serves it.
export interface ResolvedView {
  module: string;
  viewName: string;
  viewType: string;
  declaration: ViewDeclaration;
  permissions: string[];
  bundleUrl: string | null;
}

// The shape a module manifest's `navigation` array declares
// (manifest-spec.md §12's NavGroup/NavItem), merged across every loaded
// module. App-side consumers (packages/app/src/chrome) re-export these
// rather than redefining them, so the shape stays SDK-owned like every
// other schema-derived type here.
//
// `icon` is a plain name string, not a component reference —
// lucide-react/dynamic's own name→component map has no synchronous
// accessor outside its `<DynamicIcon>` lazy-loading render path, so
// resolution happens at render time via `<Icon name={...}>`
// (packages/sdk/src/components/icon.tsx), not here.
export interface NavigationItem {
  key: string;
  label: string;
  path: string;
  icon: string;
  permission?: string;
  badgeCountRoute?: string;
  // manifest-spec.md §12 NavItem.external: `path` is an external URL
  // opened in a new tab instead of a router link.
  external?: boolean;
}

export interface NavigationGroup {
  key: string;
  label: string;
  icon: string;
  module: string;
  permission?: string;
  children: NavigationItem[];
}

export interface ViewRegistry {
  resolveRoute(path: string): ResolvedView | null;
  navigationTree: NavigationGroup[];
  resources: Map<string, ResourceRegistryEntry>;
  models: Map<string, ModelDef>;
  viewPermissions(viewName: string): string[];
  getBundleUrl(moduleName: string): string | null;
  getBundleSHA256(moduleName: string): string | null;
}

const DEFAULT_GROUP_ICON = "folder";
const DEFAULT_ITEM_ICON = "circle";

// manifest-spec.md §12's NavItem/NavGroup wire schema. `looseObject`, like
// every other manifest-JSON boundary in this module — an unrecognized
// field must survive untouched, not get silently stripped.
const NavItemDeclarationSchema = v.looseObject({
  label: v.string(),
  icon: opt(v.string()),
  view: opt(v.string()),
  route: v.string(),
  permission: opt(v.string()),
  badge_count_route: opt(v.string()),
  external: opt(v.boolean()),
  // default_filters and condition are parsed as part of the manifest
  // contract but have nowhere to land yet: default_filters isn't applied
  // by any nav consumer, and no shell-side domain-expression evaluator
  // exists for condition (the same gap form-tabs.tsx's
  // resolveRecordExpression and field-renderers.tsx's computed_display
  // both already note) — an item with a condition is rendered
  // unconditionally rather than silently hidden or half-implemented.
});

const NavGroupDeclarationSchema = v.looseObject({
  label: v.string(),
  icon: opt(v.string()),
  order: v.number(),
  permission: opt(v.string()),
  children: v.array(NavItemDeclarationSchema),
});

// manifest-spec.md §12: a NavItem's `route` is relative to its module
// (e.g. "/", "/orders"); the engine's own expansion for other route
// fields (RouteSchema.path) doesn't apply here — `navigation` ships
// straight from the manifest, unexpanded. Two steps: `/{module}{route}`,
// then the shell's own `/_m` prefix for the browser URL
// (shell-architecture.md §6 "Dynamic module routes").
function expandNavPath(moduleName: string, route: string): string {
  const modulePath = route === "/" ? `/${moduleName}` : `/${moduleName}${route}`;
  return `/_m${modulePath}`;
}

function slugify(label: string): string {
  return label
    .toLowerCase()
    .replaceAll(/[^a-z0-9]+/g, "-")
    .replaceAll(/(^-|-$)/g, "");
}

// buildNavTree merges every module's `navigation` array into one tree,
// sorted by each group's own `order` (manifest-spec.md §12) — nothing
// downstream (use-navigation-tree.ts, ChromeSidebar) sorts on its own.
// Permission/module filtering happens later, in useNavigationTree — this
// only assembles and orders the raw tree.
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
          children: declaration.children.map((item) => ({
            key: `${moduleName}:${slugify(declaration.label)}:${slugify(item.label)}`,
            label: item.label,
            path: expandNavPath(moduleName, item.route),
            icon: item.icon ?? DEFAULT_ITEM_ICON,
            ...(item.permission !== undefined ? { permission: item.permission } : {}),
            ...(item.badge_count_route !== undefined ? { badgeCountRoute: item.badge_count_route } : {}),
            ...(item.external !== undefined ? { external: item.external } : {}),
          })),
        },
      });
    }
  }

  return groups.sort((a, b) => a.order - b.order).map(({ group }) => group);
}

// The manifest declares no field naming which CRUD op a column or action
// actually needs (manifest-spec.md has none, and grepping every renderer's
// own manifest-types.ts confirms none exists in code either) — the two
// concrete, unambiguous signals ResourceRegistryEntry's resolved CRUD
// paths can actually back are: the whole view, when List itself is
// missing, and a "create"-typed action or quick_create toggle, when
// Create is missing. Every other action type (route/export/import/report/
// url/custom) has no capability field to key off and is left exactly as
// declared, same as today — it already degrades by failing at request
// time (RouteActionButton/CreateActionButton) rather than being hidden
// proactively, so leaving it ungated here is not a regression.
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

// buildViewRegistry assembles shell-architecture.md §9's ViewRegistry.
// resources/models reuse buildResourceRegistry/buildModelRegistry as-is
// (goerp#638/#639) rather than re-scanning routes/models a second time.
export function buildViewRegistry(schema: MetaSchema): ViewRegistry {
  const resources = buildResourceRegistry(schema);
  const models = buildModelRegistry(schema);
  const routeMap = new Map<string, ResolvedView>();
  const permissionsByView = new Map<string, string[]>();

  for (const [moduleName, moduleSchema] of Object.entries(schema.modules)) {
    for (const route of moduleSchema.routes) {
      if (!route.view) continue;

      const declaration = resolveViewDeclaration(schema, route.view, moduleName);
      if (!declaration) continue;

      filterViewByCapability(declaration, resources.get(declaration.resource));

      const permissions = route.permissions ?? [];
      const resolved: ResolvedView = {
        module: moduleName,
        viewName: declaration.name,
        viewType: declaration.type,
        declaration,
        permissions,
        bundleUrl: moduleSchema.frontend?.bundle_url ?? null,
      };
      routeMap.set(route.path, resolved);
      permissionsByView.set(`${moduleName}.${declaration.name}`, permissions);
    }
  }

  const navigationTree = buildNavTree(schema);

  return {
    resolveRoute: (path) => routeMap.get(path) ?? null,
    navigationTree,
    resources,
    models,
    viewPermissions: (viewName) => permissionsByView.get(viewName) ?? [],
    getBundleUrl: (moduleName) => schema.modules[moduleName]?.frontend?.bundle_url ?? null,
    getBundleSHA256: (moduleName) => schema.modules[moduleName]?.frontend?.bundle_sha256 ?? null,
  };
}

// buildEmptyViewRegistry is the ViewRegistryProvider's default value before
// the first real fetch resolves (and its fallback on a fetch failure) —
// every accessor returns the same "nothing loaded yet" answer a genuinely
// empty MetaSchema would produce, so a consumer never needs to
// special-case "not loaded yet" separately from "loaded, nothing here".
export function buildEmptyViewRegistry(): ViewRegistry {
  return {
    resolveRoute: () => null,
    navigationTree: [],
    resources: new Map(),
    models: new Map(),
    viewPermissions: () => [],
    getBundleUrl: () => null,
    getBundleSHA256: () => null,
  };
}
