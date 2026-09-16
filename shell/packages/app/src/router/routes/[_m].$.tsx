import { createPermissionContextValue, permissionDataRef } from "@goerp/sdk/auth";
import { ActionButton, EmptyState, Icon, PageLayout } from "@goerp/sdk/components";
import { viewRegistryRef } from "@goerp/sdk/schema";
import { createFileRoute, notFound, redirect } from "@tanstack/react-router";
import { ensureModuleRegistered } from "../../bootstrap/module-loader.js";
import { GenericRenderer } from "../../renderers/generic-renderer.js";

// The shell's own /_m/* catch-all (shell-architecture.md §6 "Dynamic
// module routes") — the file is named `[_m].$.tsx`, not `_m.$.tsx`: a bare
// leading underscore on a route segment is TanStack Router's own
// pathless-layout convention (verified against the actual generated
// routeTree.gen.ts — `_m.$.tsx` resolves to path "/$", stripping "_m"
// entirely and catching every top-level path, not just "/_m/*"). The
// square-bracket escape is what keeps "_m" a literal, routable segment.
export const Route = createFileRoute("/_m/$")({
  beforeLoad: ({ params }) => {
    const apiPath = `/${params._splat ?? ""}`;
    const view = viewRegistryRef.current.resolveRoute(apiPath);
    if (!view) throw notFound();

    const permissions = createPermissionContextValue(permissionDataRef.current);
    if (!permissions.moduleEnabled(view.module)) throw redirect({ to: "/403" });
    if (view.permissions.length > 0 && !view.permissions.every((p) => permissions.check(p))) {
      throw redirect({ to: "/403" });
    }

    // Handed to loader via context rather than re-resolving there: the
    // registry can change between these two phases (a schema.updated/
    // module.installed WS event lands, ViewRegistryProvider swaps
    // viewRegistryRef.current and calls router.invalidate()) — re-resolving
    // in loader would read whatever the registry became by then, not what
    // was actually permission-checked just above. bundleSha256 is captured
    // here too so it can never end up paired with a bundleUrl resolved
    // from a different registry snapshot (a hot reload landing mid-navigation
    // could otherwise pair an old bundle with a new hash, or vice versa).
    return { view, bundleSha256: viewRegistryRef.current.getBundleSHA256(view.module) };
  },
  loader: async ({ context }) => {
    const { view, bundleSha256 } = context;
    await ensureModuleRegistered(view.module, view.bundleUrl, bundleSha256);
    return view;
  },
  component: RouteComponent,
  notFoundComponent: () => (
    <PageLayout>
      <EmptyState icon="file-question" title="Page not found" description="This page doesn't exist." />
    </PageLayout>
  ),
  errorComponent: ({ error, reset }) => (
    // Same full-page load-failure treatment as form-renderer.tsx's own
    // (list-renderer.md's shared "record/list load failed" design) —
    // reused here rather than extracted into a shared component neither
    // of those two already-shipped renderers otherwise needs touched for.
    <PageLayout>
      <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
        <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
        <p className="text-text">Couldn't load this page.</p>
        <p className="text-sm text-text-secondary">{error.message}</p>
        <ActionButton variant="secondary" onClick={reset}>
          Retry
        </ActionButton>
      </div>
    </PageLayout>
  ),
});

function RouteComponent() {
  const resolvedView = Route.useLoaderData();
  return <GenericRenderer resolvedView={resolvedView} />;
}
