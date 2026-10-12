import { useAuth, usePermissionsStatus } from "@goerp/sdk/auth";
import { createFileRoute, redirect } from "@tanstack/react-router";
import { OVERRIDE_PERMISSION } from "../../admin/view-overrides/view-elements.js";
import { ViewOverridesPage } from "../../admin/view-overrides/view-overrides-page.js";
import { WorkspaceLoadError, WorkspaceLoading } from "../workspace-load-state.js";

export const Route = createFileRoute("/admin/view-overrides")({
  staticData: { breadcrumb: "View overrides" },
  beforeLoad: ({ context }) => {
    const workspace = context.workspace;
    if (workspace?.permissionsStatus !== "ready") return { allowed: false };
    if (!workspace.permissions?.check(OVERRIDE_PERMISSION))
      throw redirect({ to: "/403", search: { reason: "missing_permission" } });
    return { allowed: true };
  },
  component: ViewOverridesRoute,
});

function ViewOverridesRoute() {
  const auth = useAuth();
  const status = usePermissionsStatus();
  const { allowed } = Route.useRouteContext();
  if (status === "error") return <WorkspaceLoadError />;
  if (status !== "ready" || !allowed) return <WorkspaceLoading />;
  return <ViewOverridesPage key={`${auth.tenant?.id}:${auth.user?.id}`} />;
}
