import { usePermissionsStatus } from "@goerp/sdk/auth";
import { useViewRegistryStatus } from "@goerp/sdk/schema";
import { createFileRoute, notFound, redirect, useLocation } from "@tanstack/react-router";
import { useMemo } from "react";
import { ensureModuleRegistered } from "../../bootstrap/module-loader.js";
import { NotFoundPage } from "../../pages/errors/index.js";
import { GenericRenderer } from "../../renderers/generic-renderer.js";
import { prefetchViewData } from "../../renderers/prefetch-view-data.js";
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
  loader: async ({ context, location }) => {
    const { view, bundleSha256, queryClient } = context;
    if (!view) return null;
    if (queryClient) prefetchViewData(queryClient, view, location.search);
    await ensureModuleRegistered(view.module, view.bundleUrl, bundleSha256);
    return view;
  },
  component: RouteComponent,
  notFoundComponent: NotFoundPage,
});

function RouteComponent() {
  const search = useLocation({ select: (location) => location.searchStr });
  const params = useMemo(() => new URLSearchParams(search), [search]);
  const registryStatus = useViewRegistryStatus();
  const permissionsStatus = usePermissionsStatus();
  const resolvedView = Route.useLoaderData();
  if (registryStatus === "error" || permissionsStatus === "error") return <WorkspaceLoadError />;
  if (registryStatus !== "ready" || permissionsStatus !== "ready" || !resolvedView) return <WorkspaceLoading />;
  return <GenericRenderer resolvedView={resolvedView} params={params} />;
}
