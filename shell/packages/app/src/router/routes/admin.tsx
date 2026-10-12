import { PermissionContext, useAuth } from "@goerp/sdk/auth";
import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { useContext } from "react";
import { ADMIN_NAV_GROUPS } from "../../admin/admin-nav.js";
import { isTenantAdmin } from "../../admin/is-tenant-admin.js";
import { OVERRIDE_PERMISSION } from "../../admin/view-overrides/view-elements.js";
import { SectionNav } from "../../chrome/section-nav.js";

export const Route = createFileRoute("/admin")({
  beforeLoad: ({ context, location }) => {
    const { user } = context.auth;
    if (location.pathname === "/admin/view-overrides") return;
    if (user && !isTenantAdmin(user)) throw redirect({ to: "/403", search: { reason: "missing_permission" } });
  },
  component: AdminLayout,
});

function AdminLayout() {
  const { user } = useAuth();
  const permissions = useContext(PermissionContext);
  const groups = ADMIN_NAV_GROUPS.map((group) => ({
    ...group,
    items: group.items.filter((item) =>
      item.to === "/admin/view-overrides" ? permissions?.check(OVERRIDE_PERMISSION) : isTenantAdmin(user),
    ),
  }));
  return (
    <SectionNav label="Administration" groups={groups}>
      <Outlet />
    </SectionNav>
  );
}
