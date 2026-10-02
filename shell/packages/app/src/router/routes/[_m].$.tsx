import { usePermissionsStatus } from "@goerp/sdk/auth";
import { useViewRegistryStatus } from "@goerp/sdk/schema";
import { createFileRoute, notFound, redirect } from "@tanstack/react-router";
import { ensureModuleRegistered } from "../../bootstrap/module-loader.js";
import { NotFoundPage } from "../../pages/errors/index.js";
import { GenericRenderer } from "../../renderers/generic-renderer.js";
import { WorkspaceLoadError, WorkspaceLoading } from "../workspace-load-state.js";

// Brackets prevent TanStack Router from treating _m as a pathless layout.
export const Route = createFileRoute("/_m/$")({
  beforeLoad: ({ params, context }) => {
    const workspace = context.workspace;
    if (
      workspace?.registryStatus !== "ready" ||
      workspace.permissionsStatus !== "ready" ||
      !workspace.registry ||
      !workspace.permissions
    )
      return { view: null, bundleSha256: null };
    const apiPath = `/${params._splat ?? ""}`;
    const view = workspace.registry.resolveRoute(apiPath);
    if (!view) throw notFound();

    const permissions = workspace.permissions;
    if (!permissions.moduleEnabled(view.module)) {
      throw redirect({ to: "/403", search: { reason: "module_not_enabled", module: view.module } });
    }
    if (view.permissions.length > 0 && !view.permissions.every((p) => permissions.check(p))) {
      throw redirect({ to: "/403", search: { reason: "missing_permission" } });
    }

    // Keep the permission-checked view and bundle hash from the same snapshot
    // even if a schema refresh lands before the loader runs.
    return { view, bundleSha256: workspace.registry.getBundleSHA256(view.module) };
  },
  loader: async ({ context }) => {
    const { view, bundleSha256 } = context;
    if (!view) return null;
    await ensureModuleRegistered(view.module, view.bundleUrl, bundleSha256);
    return view;
  },
  component: RouteComponent,
  notFoundComponent: NotFoundPage,
});

function RouteComponent() {
  const registryStatus = useViewRegistryStatus();
  const permissionsStatus = usePermissionsStatus();
  const resolvedView = Route.useLoaderData();
  if (registryStatus === "error" || permissionsStatus === "error") return <WorkspaceLoadError />;
  if (registryStatus !== "ready" || permissionsStatus !== "ready" || !resolvedView) return <WorkspaceLoading />;
  return <GenericRenderer resolvedView={resolvedView} />;
}
